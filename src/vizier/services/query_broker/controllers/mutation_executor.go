/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package controllers

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"px.dev/pixie/src/api/proto/uuidpb"
	"px.dev/pixie/src/api/proto/vizierpb"
	"px.dev/pixie/src/carnot/planner/distributedpb"
	"px.dev/pixie/src/carnot/planner/file_source/ir"
	"px.dev/pixie/src/carnot/planner/tetragon/ir"
	"px.dev/pixie/src/carnot/planner/plannerpb"
	"px.dev/pixie/src/carnot/planpb"
	"px.dev/pixie/src/common/base/statuspb"
	"px.dev/pixie/src/shared/services/authcontext"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/services/metadata/metadatapb"
)

// TracepointMap stores a map from the name to tracepoint info.
type TracepointMap map[string]*TracepointInfo
type FileSourceMap map[string]*FileSourceInfo
type TetragonMap map[string]*TetragonInfo

// MutationExecutor is the interface for running script mutations.
type MutationExecutor interface {
	Execute(ctx context.Context, request *vizierpb.ExecuteScriptRequest, options *planpb.PlanOptions) (*statuspb.Status, error)
	MutationInfo(ctx context.Context) (*vizierpb.MutationInfo, error)
}

// MutationExecutorImpl is responsible for running script mutations.
type MutationExecutorImpl struct {
	planner           Planner
	mdtp              metadatapb.MetadataTracepointServiceClient
	mdfs              metadatapb.MetadataFileSourceServiceClient
	mdtt              metadatapb.MetadataTetragonServiceClient
	mdconf            metadatapb.MetadataConfigServiceClient
	activeTracepoints TracepointMap
	activeFileSources FileSourceMap
	activeTetragons TetragonMap
	outputTables      []string
	distributedState  *distributedpb.DistributedState
}

// TracepointInfo stores information of a particular tracepoint.
type TracepointInfo struct {
	Name   string
	ID     uuid.UUID
	Status *statuspb.Status
}

type FileSourceInfo struct {
	GlobPattern string
	TableName   string
	ID          uuid.UUID
	Status      *statuspb.Status
}

type TetragonInfo struct {
	GlobPattern string
	TableName   string
	ID          uuid.UUID
	Status      *statuspb.Status
}

// NewMutationExecutor creates a new mutation executor.
func NewMutationExecutor(
	planner Planner,
	mdtp metadatapb.MetadataTracepointServiceClient,
	mdfs metadatapb.MetadataFileSourceServiceClient,
	mdtt metadatapb.MetadataTetragonServiceClient,
	mdconf metadatapb.MetadataConfigServiceClient,
	distributedState *distributedpb.DistributedState,
) MutationExecutor {
	return &MutationExecutorImpl{
		planner:           planner,
		mdtp:              mdtp,
		mdfs:              mdfs,
		mdtt:              mdtt,
		mdconf:            mdconf,
		distributedState:  distributedState,
		activeTracepoints: make(TracepointMap),
		activeFileSources: make(FileSourceMap),
		activeTetragons: make(TetragonMap),
	}
}

// Execute runs the mutation. On unknown errors it will return an error, otherwise we return a status message
// that has more context about the error message.
func (m *MutationExecutorImpl) Execute(ctx context.Context, req *vizierpb.ExecuteScriptRequest, planOpts *planpb.PlanOptions) (*statuspb.Status, error) {
	convertedReq, err := VizierQueryRequestToPlannerMutationRequest(req)
	if err != nil {
		return nil, err
	}
	var otelConfig *distributedpb.OTelEndpointConfig
	if convertedReq.Configs != nil && convertedReq.Configs.OTelEndpointConfig != nil {
		otelConfig = &distributedpb.OTelEndpointConfig{
			URL:      convertedReq.Configs.OTelEndpointConfig.URL,
			Headers:  convertedReq.Configs.OTelEndpointConfig.Headers,
			Insecure: convertedReq.Configs.OTelEndpointConfig.Insecure,
			Timeout:  convertedReq.Configs.OTelEndpointConfig.Timeout,
		}
	}
	var pluginConfig *distributedpb.PluginConfig
	if req.Configs != nil && req.Configs.PluginConfig != nil {
		pluginConfig = &distributedpb.PluginConfig{
			StartTimeNs: req.Configs.PluginConfig.StartTimeNs,
			EndTimeNs:   req.Configs.PluginConfig.EndTimeNs,
		}
	}
	convertedReq.LogicalPlannerState = &distributedpb.LogicalPlannerState{
		DistributedState:   m.distributedState,
		PlanOptions:        planOpts,
		OTelEndpointConfig: otelConfig,
		PluginConfig:       pluginConfig,
	}

	mutations, err := m.planner.CompileMutations(convertedReq)
	if err != nil {
		log.WithError(err).Error("Got an error while compiling mutations")
		return nil, err
	}
	if mutations.Status != nil && mutations.Status.ErrCode != statuspb.OK {
		return mutations.Status, nil
	}
	aCtx, err := authcontext.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", fmt.Sprintf("bearer %s", aCtx.AuthToken))

	if len(mutations.Mutations) == 0 {
		// No mutations to apply.
		return nil, nil
	}

	registerTracepointsReq := &metadatapb.RegisterTracepointRequest{
		Requests: make([]*metadatapb.RegisterTracepointRequest_TracepointRequest, 0),
	}
	deleteTracepointsReq := &metadatapb.RemoveTracepointRequest{
		Names: make([]string, 0),
	}
	configmapReqs := make([]*metadatapb.UpdateConfigRequest, 0)
	fileSourceReqs := &metadatapb.RegisterFileSourceRequest{
		Requests: make([]*ir.FileSourceDeployment, 0),
	}
	deleteFileSourcesReq := &metadatapb.RemoveFileSourceRequest{
		Names: make([]string, 0),
	}
	TetragonReqs := &metadatapb.RegisterTetragonRequest{
		Requests: make([]*ir.TetragonDeployment, 0),
	}
	deleteTetragonsReq := &metadatapb.RemoveTetragonRequest{
		Names: make([]string, 0),
	}

	outputTablesMap := make(map[string]bool)
	// TODO(zasgar): We should make sure that we don't simultaneously add and delete the tracepoint.
	// While this will probably work, we should restrict this because it's likely not the intended behavior.
	for _, mut := range mutations.Mutations {
		switch mut := mut.Mutation.(type) {
		case *plannerpb.CompileMutation_Trace:
			{
				name := mut.Trace.Name
				registerTracepointsReq.Requests = append(registerTracepointsReq.Requests,
					&metadatapb.RegisterTracepointRequest_TracepointRequest{
						TracepointDeployment: mut.Trace,
						Name:                 mut.Trace.Name,
						TTL:                  mut.Trace.TTL,
					})

				if _, ok := m.activeTracepoints[name]; ok {
					return nil, fmt.Errorf("tracepoint with name '%s', already used", name)
				}
				for _, tracepoint := range mut.Trace.Programs {
					outputTablesMap[tracepoint.TableName] = true
				}

				m.activeTracepoints[name] = &TracepointInfo{
					Name:   name,
					ID:     uuid.Nil,
					Status: nil,
				}
			}
		case *plannerpb.CompileMutation_DeleteTracepoint:
			{
				deleteTracepointsReq.Names = append(deleteTracepointsReq.Names, mut.DeleteTracepoint.Name)
			}
		case *plannerpb.CompileMutation_ConfigUpdate:
			{
				configmapReqs = append(configmapReqs, &metadatapb.UpdateConfigRequest{
					Key:          mut.ConfigUpdate.Key,
					Value:        mut.ConfigUpdate.Value,
					AgentPodName: mut.ConfigUpdate.AgentPodName,
				})
			}
		case *plannerpb.CompileMutation_FileSource:
			{
				name := mut.FileSource.GlobPattern
				tableName := mut.FileSource.TableName
				fileSourceReqs.Requests = append(fileSourceReqs.Requests, &ir.FileSourceDeployment{
					Name:        name,
					GlobPattern: name,
					TableName:   tableName,
					TTL:         mut.FileSource.TTL,
				})
				if _, ok := m.activeFileSources[name]; ok {
					return nil, fmt.Errorf("file source with name '%s', already used", name)
				}
				// TODO(ddelnano): Add unit tests that would have caught the bug with the
				// file source output table issue. The line that caused the bug is left commented below:
				// outputTablesMap[name] = true
				outputTablesMap[tableName] = true

				m.activeFileSources[name] = &FileSourceInfo{
					GlobPattern: mut.FileSource.GlobPattern,
					ID:          uuid.Nil,
					Status:      nil,
				}
			}
		case *plannerpb.CompileMutation_DeleteFileSource:
			{
				deleteFileSourcesReq.Names = append(deleteFileSourcesReq.Names, mut.DeleteFileSource.GlobPattern)
			}
		case *plannerpb.CompileMutation_Tetragon:
			{
				name := mut.Tetragon.GlobPattern
				tableName := mut.Tetragon.TableName
				tetragonReqs.Requests = append(tetragonReqs.Requests, &ir.TetragonDeployment{
					Name:        name,
					GlobPattern: name,
					TableName:   tableName,
					TTL:         mut.Tetragon.TTL,
				})
				if _, ok := m.activeTetragons[name]; ok {
					return nil, fmt.Errorf("file source with name '%s', already used", name)
				}
				// TODO(ddelnano): Add unit tests that would have caught the bug with the
				// file source output table issue. The line that caused the bug is left commented below:
				// outputTablesMap[name] = true
				outputTablesMap[tableName] = true

				m.activeTetragons[name] = &TetragonInfo{
					GlobPattern: mut.Tetragon.GlobPattern,
					ID:          uuid.Nil,
					Status:      nil,
				}
			}
		case *plannerpb.CompileMutation_DeleteTetragon:
			{
				deleteTetragonsReq.Names = append(deleteTetragonsReq.Names, mut.DeleteTetragon.GlobPattern)
			}
		}
	}

	if len(registerTracepointsReq.Requests) > 0 {
		resp, err := m.mdtp.RegisterTracepoint(ctx, registerTracepointsReq)
		if err != nil {
			log.WithError(err).
				Errorf("Failed to register tracepoints")
			return nil, ErrTracepointRegistrationFailed
		}
		if resp.Status != nil && resp.Status.ErrCode != statuspb.OK {
			log.WithField("status", resp.Status.String()).
				Errorf("Failed to register tracepoints with bad status")

			return resp.Status, ErrTracepointRegistrationFailed
		}

		// Update the internal stat of the tracepoints.
		for _, tp := range resp.Tracepoints {
			id := utils.UUIDFromProtoOrNil(tp.ID)
			m.activeTracepoints[tp.Name].ID = id
			m.activeTracepoints[tp.Name].Status = tp.Status
		}
	}
	if len(deleteTracepointsReq.Names) > 0 {
		delResp, err := m.mdtp.RemoveTracepoint(ctx, deleteTracepointsReq)
		if err != nil {
			log.WithError(err).
				Errorf("Failed to delete tracepoints")
			return nil, ErrTracepointDeletionFailed
		}
		if delResp.Status != nil && delResp.Status.ErrCode != statuspb.OK {
			log.WithField("status", delResp.Status.String()).
				Errorf("Failed to delete tracepoints with bad status")
			return delResp.Status, ErrTracepointDeletionFailed
		}
		// Remove the tracepoints we considered deleted.
		for _, tpName := range deleteTracepointsReq.Names {
			delete(m.activeTracepoints, tpName)
		}
	}

	if len(configmapReqs) > 0 {
		for _, configmapReq := range configmapReqs {
			resp, err := m.mdconf.UpdateConfig(ctx, configmapReq)
			if err != nil || (resp.Status != nil && resp.Status.ErrCode != statuspb.OK) {
				return nil, ErrConfigUpdateFailed
			}
		}
	}

	if len(fileSourceReqs.Requests) > 0 {
		resp, err := m.mdfs.RegisterFileSource(ctx, fileSourceReqs)
		if err != nil {
			log.WithError(err).
				Errorf("Failed to register file sources")
			return nil, ErrFileSourceRegistrationFailed
		}
		if resp.Status != nil && resp.Status.ErrCode != statuspb.OK {
			log.WithField("status", resp.Status.String()).
				Errorf("Failed to register file sources with bad status")
			return resp.Status, ErrFileSourceRegistrationFailed
		}

		// Update the internal stat of the file sources.
		for _, fs := range resp.FileSources {
			id := utils.UUIDFromProtoOrNil(fs.ID)
			m.activeFileSources[fs.Name].ID = id
			m.activeFileSources[fs.Name].Status = fs.Status
		}
	}
	if len(deleteFileSourcesReq.Names) > 0 {
		delResp, err := m.mdfs.RemoveFileSource(ctx, deleteFileSourcesReq)
		if err != nil {
			log.WithError(err).
				Errorf("Failed to delete tracepoints")
			return nil, ErrFileSourceDeletionFailed
		}
		if delResp.Status != nil && delResp.Status.ErrCode != statuspb.OK {
			log.WithField("status", delResp.Status.String()).
				Errorf("Failed to delete tracepoints with bad status")
			return delResp.Status, ErrFileSourceDeletionFailed
		}
		// Remove the tracepoints we considered deleted.
		for _, fsName := range deleteFileSourcesReq.Names {
			delete(m.activeFileSources, fsName)
		}
	}

	if len(TetragonReqs.Requests) > 0 {
		resp, err := m.mdtt.RegisterTetragon(ctx, tetragonReqs)
		if err != nil {
			log.WithError(err).
				Errorf("Failed to register tetragons")
			return nil, ErrTetragonRegistrationFailed
		}
		if resp.Status != nil && resp.Status.ErrCode != statuspb.OK {
			log.WithField("status", resp.Status.String()).
				Errorf("Failed to register tetragons with bad status")
			return resp.Status, ErrTetragonRegistrationFailed
		}

		// Update the internal stat of the tetragons.
		for _, tt := range resp.Tetragons {
			id := utils.UUIDFromProtoOrNil(tt.ID)
			m.activeTetragons[tt.Name].ID = id
			m.activeTetragons[tt.Name].Status = tt.Status
		}
	}
	if len(deleteTetragonsReq.Names) > 0 {
		delResp, err := m.mdtt.RemoveTetragon(ctx, deleteTetragonsReq)
		if err != nil {
			log.WithError(err).
				Errorf("Failed to delete tetragons")
			return nil, ErrTetragonDeletionFailed
		}
		if delResp.Status != nil && delResp.Status.ErrCode != statuspb.OK {
			log.WithField("status", delResp.Status.String()).
				Errorf("Failed to delete tetragons with bad status")
			return delResp.Status, ErrTetragonDeletionFailed
		}
		// Remove the tetragons we considered deleted.
		for _, ttName := range deleteTetragonsReq.Names {
			delete(m.activeTetragons, ttName)
		}
	}

	m.outputTables = make([]string, 0)
	for k := range outputTablesMap {
		m.outputTables = append(m.outputTables, k)
	}

	return nil, nil
}

// MutationInfo returns the summarized mutation information.
func (m *MutationExecutorImpl) MutationInfo(ctx context.Context) (*vizierpb.MutationInfo, error) {
	tpReq := &metadatapb.GetTracepointInfoRequest{
		IDs: make([]*uuidpb.UUID, 0),
	}
	for _, tp := range m.activeTracepoints {
		tpReq.IDs = append(tpReq.IDs, utils.ProtoFromUUID(tp.ID))
	}
	fsReq := &metadatapb.GetFileSourceInfoRequest{
		IDs: make([]*uuidpb.UUID, 0),
	}
	for _, fs := range m.activeFileSources {
		fsReq.IDs = append(fsReq.IDs, utils.ProtoFromUUID(fs.ID))
	}
	ttReq := &metadatapb.GetTetragonInfoRequest{
		IDs: make([]*uuidpb.UUID, 0),
	}
	for _, tt := range m.activeTetragons {
		ttReq.IDs = append(ttReq.IDs, utils.ProtoFromUUID(tt.ID))
	}
	aCtx, err := authcontext.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", fmt.Sprintf("bearer %s", aCtx.AuthToken))

	tpResp, err := m.mdtp.GetTracepointInfo(ctx, tpReq)
	if err != nil {
		return nil, err
	}
	fsResp, err := m.mdfs.GetFileSourceInfo(ctx, fsReq)
	if err != nil {
		return nil, err
	}
	ttResp, err := m.mdfs.GetTetragonInfo(ctx, ttReq)
	if err != nil {
		return nil, err
	}
	tps := len(tpResp.Tracepoints)
	mutationInfo := &vizierpb.MutationInfo{
		Status: &vizierpb.Status{Code: 0},
		States: make([]*vizierpb.MutationInfo_MutationState, tps+len(fsResp.FileSources)+len(ttResp.Tetragons)),
	}

	tpReady := true
	for idx, tp := range tpResp.Tracepoints {
		mutationInfo.States[idx] = &vizierpb.MutationInfo_MutationState{
			ID:    utils.UUIDFromProtoOrNil(tp.ID).String(),
			State: convertLifeCycleStateToVizierLifeCycleState(tp.State),
			Name:  tp.Name,
		}
		if tp.State != statuspb.RUNNING_STATE {
			tpReady = false
		}
	}

	fsReady := true
	for idx, fs := range fsResp.FileSources {
		mutationInfo.States[idx+tps] = &vizierpb.MutationInfo_MutationState{
			ID:    utils.UUIDFromProtoOrNil(fs.ID).String(),
			State: convertLifeCycleStateToVizierLifeCycleState(fs.State),
			Name:  fs.Name,
		}
		if fs.State != statuspb.RUNNING_STATE {
			fsReady = false
		}
	}

	ttReady := true
	for idx, tt := range ttResp.Tetragons {
		mutationInfo.States[idx+tps] = &vizierpb.MutationInfo_MutationState{
			ID:    utils.UUIDFromProtoOrNil(tt.ID).String(),
			State: convertLifeCycleStateToVizierLifeCycleState(tt.State),
			Name:  tt.Name,
		}
		if tt.State != statuspb.RUNNING_STATE {
			ttReady = false
		}
	}

	if !tpReady {
		mutationInfo.Status = &vizierpb.Status{
			Code:    int32(codes.Unavailable),
			Message: "probe installation in progress",
		}
		return mutationInfo, nil
	}

	if !fsReady {
		mutationInfo.Status = &vizierpb.Status{
			Code:    int32(codes.Unavailable),
			Message: "file source installation in progress",
		}
		return mutationInfo, nil
	}

	if !m.isSchemaReady() {
		mutationInfo.Status = &vizierpb.Status{
			Code:    int32(codes.Unavailable),
			Message: "Schema is not ready yet",
		}
	}
	return mutationInfo, nil
}

func (m *MutationExecutorImpl) isSchemaReady() bool {
	schemaNames := make(map[string]bool)
	for _, s := range m.distributedState.SchemaInfo {
		schemaNames[s.Name] = true
	}
	for _, s := range m.outputTables {
		if _, ok := schemaNames[s]; !ok {
			return false
		}
	}
	return true
}
