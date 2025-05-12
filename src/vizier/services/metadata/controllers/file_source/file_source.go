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

package file_source

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"px.dev/pixie/src/api/proto/uuidpb"
	"px.dev/pixie/src/carnot/planner/file_source/ir"
	"px.dev/pixie/src/common/base/statuspb"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/messages/messagespb"
	"px.dev/pixie/src/vizier/services/metadata/storepb"
	"px.dev/pixie/src/vizier/services/shared/agentpb"
)

var (
	// ErrFileSourceAlreadyExists is produced if a file_source already exists with the given name
	// and does not have a matching schema.
	ErrFileSourceAlreadyExists = errors.New("FileSource already exists")
)

// agentMessenger is a controller that lets us message all agents and all active agents.
type agentMessenger interface {
	MessageAgents(agentIDs []uuid.UUID, msg []byte) error
	MessageActiveAgents(msg []byte) error
}

// Store is a datastore which can store, update, and retrieve information about file_sources.
type Store interface {
	UpsertFileSource(uuid.UUID, *storepb.FileSourceInfo) error
	GetFileSource(uuid.UUID) (*storepb.FileSourceInfo, error)
	GetFileSources() ([]*storepb.FileSourceInfo, error)
	UpdateFileSourceState(*storepb.AgentFileSourceStatus) error
	GetFileSourceStates(uuid.UUID) ([]*storepb.AgentFileSourceStatus, error)
	SetFileSourceWithName(string, uuid.UUID) error
	GetFileSourcesWithNames([]string) ([]*uuid.UUID, error)
	GetFileSourcesForIDs([]uuid.UUID) ([]*storepb.FileSourceInfo, error)
	SetFileSourceTTL(uuid.UUID, time.Duration) error
	DeleteFileSourceTTLs([]uuid.UUID) error
	DeleteFileSource(uuid.UUID) error
	DeleteFileSourcesForAgent(uuid.UUID) error
	GetFileSourceTTLs() ([]uuid.UUID, []time.Time, error)
}

// Manager manages the file_sources deployed in the cluster.
type Manager struct {
	ts     Store
	agtMgr agentMessenger

	done chan struct{}
	once sync.Once
}

// NewManager creates a new file_source manager.
func NewManager(ts Store, agtMgr agentMessenger, ttlReaperDuration time.Duration) *Manager {
	tm := &Manager{
		ts:     ts,
		agtMgr: agtMgr,
		done:   make(chan struct{}),
	}

	go tm.watchForFileSourceExpiry(ttlReaperDuration)
	return tm
}

func (m *Manager) watchForFileSourceExpiry(ttlReaperDuration time.Duration) {
	ticker := time.NewTicker(ttlReaperDuration)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.terminateExpiredFileSources()
		}
	}
}

func (m *Manager) terminateExpiredFileSources() {
	fss, err := m.ts.GetFileSources()
	if err != nil {
		log.WithError(err).Warn("error encountered when trying to terminating expired file_sources")
		return
	}

	ttlKeys, ttlVals, err := m.ts.GetFileSourceTTLs()
	if err != nil {
		log.WithError(err).Warn("error encountered when trying to terminating expired file_sources")
		return
	}

	now := time.Now()

	// Lookup for file_sources that still have an active ttl
	fsActive := make(map[uuid.UUID]bool)
	for i, fs := range ttlKeys {
		fsActive[fs] = ttlVals[i].After(now)
	}

	for _, fs := range fss {
		fsID := utils.UUIDFromProtoOrNil(fs.ID)
		if fsActive[fsID] {
			// FileSource TTL exists and is in the future
			continue
		}
		if fs.ExpectedState == statuspb.TERMINATED_STATE {
			// FileSource is already in terminated state
			continue
		}
		err = m.terminateFileSource(fsID)
		if err != nil {
			log.WithError(err).Warn("error encountered when trying to terminating expired file_sources")
		}
	}
}

func (m *Manager) terminateFileSource(id uuid.UUID) error {
	// Update state in datastore to terminated.
	fs, err := m.ts.GetFileSource(id)
	if err != nil {
		return err
	}

	if fs == nil {
		return nil
	}

	fs.ExpectedState = statuspb.TERMINATED_STATE
	err = m.ts.UpsertFileSource(id, fs)
	if err != nil {
		return err
	}

	// Send termination messages to PEMs.
	fileSourceReq := messagespb.VizierMessage{
		Msg: &messagespb.VizierMessage_FileSourceMessage{
			FileSourceMessage: &messagespb.FileSourceMessage{
				Msg: &messagespb.FileSourceMessage_RemoveFileSourceRequest{
					RemoveFileSourceRequest: &messagespb.RemoveFileSourceRequest{
						ID: utils.ProtoFromUUID(id),
					},
				},
			},
		},
	}
	msg, err := fileSourceReq.Marshal()
	if err != nil {
		return err
	}

	return m.agtMgr.MessageActiveAgents(msg)
}

func (m *Manager) deleteFileSource(id uuid.UUID) error {
	return m.ts.DeleteFileSource(id)
}

// CreateFileSource creates and stores info about the given file source.
func (m *Manager) CreateFileSource(fileSourceName string, fileSourceDeployment *ir.FileSourceDeployment) (*uuid.UUID, error) {
	// Check to see if a file source with the matching name already exists.
	resp, err := m.ts.GetFileSourcesWithNames([]string{fileSourceName})
	if err != nil {
		return nil, err
	}

	if len(resp) != 1 {
		return nil, errors.New("Could not fetch fileSource")
	}
	prevFileSourceID := resp[0]

	ttl, err := types.DurationFromProto(fileSourceDeployment.TTL)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to parse duration: %+v", err))
	}

	if prevFileSourceID != nil { // Existing file source already exists.
		prevFileSource, err := m.ts.GetFileSource(*prevFileSourceID)
		if err != nil {
			return nil, err
		}
		if prevFileSource != nil && prevFileSource.ExpectedState != statuspb.TERMINATED_STATE {
			// If everything is exactly the same, no need to redeploy
			//   - return prevFileSourceID, ErrFileSourceAlreadyExists
			// If anything inside file sources has changed
			//   - delete old file sources, and insert new file sources.

			// Check if the file sources are exactly the same.
			allFsSame := true
			if !proto.Equal(prevFileSource.FileSource, fileSourceDeployment) {
				allFsSame = false
			}

			if allFsSame {
				err = m.ts.SetFileSourceTTL(*prevFileSourceID, ttl)
				if err != nil {
					return nil, err
				}
				return prevFileSourceID, ErrFileSourceAlreadyExists
			}

			// Something has changed, so trigger termination of the old file source.
			err = m.ts.DeleteFileSourceTTLs([]uuid.UUID{*prevFileSourceID})
			if err != nil {
				return nil, err
			}
		}
	}

	fsID, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	newFileSource := &storepb.FileSourceInfo{
		ID:            utils.ProtoFromUUID(fsID),
		Name:          fileSourceName,
		FileSource:    fileSourceDeployment,
		ExpectedState: statuspb.RUNNING_STATE,
	}
	err = m.ts.UpsertFileSource(fsID, newFileSource)
	if err != nil {
		return nil, err
	}
	err = m.ts.SetFileSourceTTL(fsID, ttl)
	if err != nil {
		return nil, err
	}
	err = m.ts.SetFileSourceWithName(fileSourceName, fsID)
	if err != nil {
		return nil, err
	}
	return &fsID, nil
}

// GetAllFileSources gets all the file sources currently tracked by the metadata service.
func (m *Manager) GetAllFileSources() ([]*storepb.FileSourceInfo, error) {
	return m.ts.GetFileSources()
}

// UpdateAgentFileSourceStatus updates the file source info with the new agent file source status.
func (m *Manager) UpdateAgentFileSourceStatus(fileSourceID *uuidpb.UUID, agentID *uuidpb.UUID, state statuspb.LifeCycleState, status *statuspb.Status) error {
	if state == statuspb.TERMINATED_STATE { // If all agent file source statuses are now terminated, we can finally delete the file source from the datastore.
		tID := utils.UUIDFromProtoOrNil(fileSourceID)
		states, err := m.GetFileSourceStates(tID)
		if err != nil {
			return err
		}
		allTerminated := true
		for _, s := range states {
			if s.State != statuspb.TERMINATED_STATE && !s.AgentID.Equal(agentID) {
				allTerminated = false
				break
			}
		}

		if allTerminated {
			return m.deleteFileSource(tID)
		}
	}

	fileSourceState := &storepb.AgentFileSourceStatus{
		State:   state,
		Status:  status,
		ID:      fileSourceID,
		AgentID: agentID,
	}

	return m.ts.UpdateFileSourceState(fileSourceState)
}

// RegisterFileSource sends requests to the given agents to register the specified file source.
func (m *Manager) RegisterFileSource(agents []*agentpb.Agent, fileSourceID uuid.UUID, fileSourceDeployment *ir.FileSourceDeployment) error {
	agentIDs := make([]uuid.UUID, len(agents))
	fileSourceReq := messagespb.VizierMessage{
		Msg: &messagespb.VizierMessage_FileSourceMessage{
			FileSourceMessage: &messagespb.FileSourceMessage{
				Msg: &messagespb.FileSourceMessage_RegisterFileSourceRequest{
					RegisterFileSourceRequest: &messagespb.RegisterFileSourceRequest{
						FileSourceDeployment: fileSourceDeployment,
						ID:                   utils.ProtoFromUUID(fileSourceID),
					},
				},
			},
		},
	}
	msg, err := fileSourceReq.Marshal()
	if err != nil {
		return err
	}
	for i, agt := range agents {
		agentIDs[i] = utils.UUIDFromProtoOrNil(agt.Info.AgentID)
	}

	err = m.agtMgr.MessageAgents(agentIDs, msg)

	if err != nil {
		return err
	}

	return nil
}

// GetFileSourceInfo gets the status for the file source with the given ID.
func (m *Manager) GetFileSourceInfo(fileSourceID uuid.UUID) (*storepb.FileSourceInfo, error) {
	return m.ts.GetFileSource(fileSourceID)
}

// GetFileSourceStates gets all the known agent states for the given file source.
func (m *Manager) GetFileSourceStates(fileSourceID uuid.UUID) ([]*storepb.AgentFileSourceStatus, error) {
	return m.ts.GetFileSourceStates(fileSourceID)
}

// GetFileSourcesForIDs gets all the file source infos for the given ids.
func (m *Manager) GetFileSourcesForIDs(ids []uuid.UUID) ([]*storepb.FileSourceInfo, error) {
	return m.ts.GetFileSourcesForIDs(ids)
}

// RemoveFileSources starts the termination process for the file sources with the given names.
func (m *Manager) RemoveFileSources(names []string) error {
	fsIDs, err := m.ts.GetFileSourcesWithNames(names)
	if err != nil {
		return err
	}

	ids := make([]uuid.UUID, len(fsIDs))

	for i, id := range fsIDs {
		if id == nil {
			return fmt.Errorf("Could not find file source for given name: %s", names[i])
		}
		ids[i] = *id
	}

	return m.ts.DeleteFileSourceTTLs(ids)
}

// DeleteAgent deletes file sources on the given agent.
func (m *Manager) DeleteAgent(agentID uuid.UUID) error {
	return m.ts.DeleteFileSourcesForAgent(agentID)
}

// Close cleans up the goroutines created and renders this no longer useable.
func (m *Manager) Close() {
	m.once.Do(func() {
		close(m.done)
	})
	m.ts = nil
	m.agtMgr = nil
}
