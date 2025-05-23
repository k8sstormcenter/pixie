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

package tetragon

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
	"px.dev/pixie/src/carnot/planner/tetragon/ir"
	"px.dev/pixie/src/common/base/statuspb"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/messages/messagespb"
	"px.dev/pixie/src/vizier/services/metadata/storepb"
	"px.dev/pixie/src/vizier/services/shared/agentpb"
)

var (
	// ErrTetragonAlreadyExists is produced if a tetragon already exists with the given name
	// and does not have a matching schema.
	ErrTetragonAlreadyExists = errors.New("Tetragon already exists")
)

// agentMessenger is a controller that lets us message all agents and all active agents.
type agentMessenger interface {
	MessageAgents(agentIDs []uuid.UUID, msg []byte) error
	MessageActiveAgents(msg []byte) error
}

// Store is a datastore which can store, update, and retrieve information about tetragons.
type Store interface {
	UpsertTetragon(uuid.UUID, *storepb.TetragonInfo) error
	GetTetragon(uuid.UUID) (*storepb.TetragonInfo, error)
	GetTetragons() ([]*storepb.TetragonInfo, error)
	UpdateTetragonState(*storepb.AgentTetragonStatus) error
	GetTetragonStates(uuid.UUID) ([]*storepb.AgentTetragonStatus, error)
	SetTetragonWithName(string, uuid.UUID) error
	GetTetragonsWithNames([]string) ([]*uuid.UUID, error)
	GetTetragonsForIDs([]uuid.UUID) ([]*storepb.TetragonInfo, error)
	SetTetragonTTL(uuid.UUID, time.Duration) error
	DeleteTetragonTTLs([]uuid.UUID) error
	DeleteTetragon(uuid.UUID) error
	DeleteTetragonsForAgent(uuid.UUID) error
	GetTetragonTTLs() ([]uuid.UUID, []time.Time, error)
}

// Manager manages the tetragons deployed in the cluster.
type Manager struct {
	ts     Store
	agtMgr agentMessenger

	done chan struct{}
	once sync.Once
}

// NewManager creates a new tetragon manager.
func NewManager(ts Store, agtMgr agentMessenger, ttlReaperDuration time.Duration) *Manager {
	tm := &Manager{
		ts:     ts,
		agtMgr: agtMgr,
		done:   make(chan struct{}),
	}

	go tm.watchForTetragonExpiry(ttlReaperDuration)
	return tm
}

func (m *Manager) watchForTetragonExpiry(ttlReaperDuration time.Duration) {
	ticker := time.NewTicker(ttlReaperDuration)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.terminateExpiredTetragons()
		}
	}
}

func (m *Manager) terminateExpiredTetragons() {
	fss, err := m.ts.GetTetragons()
	if err != nil {
		log.WithError(err).Warn("error encountered when trying to terminating expired tetragons")
		return
	}

	ttlKeys, ttlVals, err := m.ts.GetTetragonTTLs()
	if err != nil {
		log.WithError(err).Warn("error encountered when trying to terminating expired tetragons")
		return
	}

	now := time.Now()

	// Lookup for tetragons that still have an active ttl
	fsActive := make(map[uuid.UUID]bool)
	for i, fs := range ttlKeys {
		fsActive[fs] = ttlVals[i].After(now)
	}

	for _, fs := range fss {
		fsID := utils.UUIDFromProtoOrNil(fs.ID)
		if fsActive[fsID] {
			// Tetragon TTL exists and is in the future
			continue
		}
		if fs.ExpectedState == statuspb.TERMINATED_STATE {
			// Tetragon is already in terminated state
			continue
		}
		err = m.terminateTetragon(fsID)
		if err != nil {
			log.WithError(err).Warn("error encountered when trying to terminating expired tetragons")
		}
	}
}

func (m *Manager) terminateTetragon(id uuid.UUID) error {
	// Update state in datastore to terminated.
	fs, err := m.ts.GetTetragon(id)
	if err != nil {
		return err
	}

	if fs == nil {
		return nil
	}

	fs.ExpectedState = statuspb.TERMINATED_STATE
	err = m.ts.UpsertTetragon(id, fs)
	if err != nil {
		return err
	}

	// Send termination messages to PEMs.
	tetragonReq := messagespb.VizierMessage{
		Msg: &messagespb.VizierMessage_TetragonMessage{
			TetragonMessage: &messagespb.TetragonMessage{
				Msg: &messagespb.TetragonMessage_RemoveTetragonRequest{
					RemoveTetragonRequest: &messagespb.RemoveTetragonRequest{
						ID: utils.ProtoFromUUID(id),
					},
				},
			},
		},
	}
	msg, err := tetragonReq.Marshal()
	if err != nil {
		return err
	}

	return m.agtMgr.MessageActiveAgents(msg)
}

func (m *Manager) deleteTetragon(id uuid.UUID) error {
	return m.ts.DeleteTetragon(id)
}

// CreateTetragon creates and stores info about the given tetragon.
func (m *Manager) CreateTetragon(tetragonName string, tetragonDeployment *ir.TetragonDeployment) (*uuid.UUID, error) {
	// Check to see if a tetragon with the matching name already exists.
	resp, err := m.ts.GetTetragonsWithNames([]string{tetragonName})
	if err != nil {
		return nil, err
	}

	if len(resp) != 1 {
		return nil, errors.New("Could not fetch tetragon")
	}
	prevTetragonID := resp[0]

	ttl, err := types.DurationFromProto(tetragonDeployment.TTL)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to parse duration: %+v", err))
	}

	if prevTetragonID != nil { // Existing tetragon already exists.
		prevTetragon, err := m.ts.GetTetragon(*prevTetragonID)
		if err != nil {
			return nil, err
		}
		if prevTetragon != nil && prevTetragon.ExpectedState != statuspb.TERMINATED_STATE {
			// If everything is exactly the same, no need to redeploy
			//   - return prevTetragonID, ErrTetragonAlreadyExists
			// If anything inside tetragons has changed
			//   - delete old tetragons, and insert new tetragons.

			// Check if the tetragons are exactly the same.
			allFsSame := true
			if !proto.Equal(prevTetragon.Tetragon, tetragonDeployment) {
				allFsSame = false
			}

			if allFsSame {
				err = m.ts.SetTetragonTTL(*prevTetragonID, ttl)
				if err != nil {
					return nil, err
				}
				return prevTetragonID, ErrTetragonAlreadyExists
			}

			// Something has changed, so trigger termination of the old tetragon.
			err = m.ts.DeleteTetragonTTLs([]uuid.UUID{*prevTetragonID})
			if err != nil {
				return nil, err
			}
		}
	}

	fsID, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	newTetragon := &storepb.TetragonInfo{
		ID:            utils.ProtoFromUUID(fsID),
		Name:          TetragonName,
		Tetragon:    TetragonDeployment,
		ExpectedState: statuspb.RUNNING_STATE,
	}
	err = m.ts.UpsertTetragon(fsID, newTetragon)
	if err != nil {
		return nil, err
	}
	err = m.ts.SetTetragonTTL(fsID, ttl)
	if err != nil {
		return nil, err
	}
	err = m.ts.SetTetragonWithName(tetragonName, fsID)
	if err != nil {
		return nil, err
	}
	return &fsID, nil
}

// GetAllTetragons gets all the tetragons currently tracked by the metadata service.
func (m *Manager) GetAllTetragons() ([]*storepb.TetragonInfo, error) {
	return m.ts.GetTetragons()
}

// UpdateAgentTetragonStatus updates the tetragon info with the new agent tetragon status.
func (m *Manager) UpdateAgentTetragonStatus(tetragonID *uuidpb.UUID, agentID *uuidpb.UUID, state statuspb.LifeCycleState, status *statuspb.Status) error {
	if state == statuspb.TERMINATED_STATE { // If all agent tetragon statuses are now terminated, we can finally delete the tetragon from the datastore.
		tID := utils.UUIDFromProtoOrNil(tetragonID)
		states, err := m.GetTetragonStates(tID)
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
			return m.deleteTetragon(tID)
		}
	}

	tetragonState := &storepb.AgentTetragonStatus{
		State:   state,
		Status:  status,
		ID:      tetragonID,
		AgentID: agentID,
	}

	return m.ts.UpdateTetragonState(tetragonState)
}

// RegisterTetragon sends requests to the given agents to register the specified tetragon.
func (m *Manager) RegisterTetragon(agents []*agentpb.Agent, tetragonID uuid.UUID, tetragonDeployment *ir.TetragonDeployment) error {
	agentIDs := make([]uuid.UUID, len(agents))
	tetragonReq := messagespb.VizierMessage{
		Msg: &messagespb.VizierMessage_TetragonMessage{
			TetragonMessage: &messagespb.TetragonMessage{
				Msg: &messagespb.TetragonMessage_RegisterTetragonRequest{
					RegisterTetragonRequest: &messagespb.RegisterTetragonRequest{
						TetragonDeployment: TetragonDeployment,
						ID:                   utils.ProtoFromUUID(tetragonID),
					},
				},
			},
		},
	}
	msg, err := tetragonReq.Marshal()
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

// GetTetragonInfo gets the status for the tetragon with the given ID.
func (m *Manager) GetTetragonInfo(tetragonID uuid.UUID) (*storepb.TetragonInfo, error) {
	return m.ts.GetTetragon(tetragonID)
}

// GetTetragonStates gets all the known agent states for the given tetragon.
func (m *Manager) GetTetragonStates(tetragonID uuid.UUID) ([]*storepb.AgentTetragonStatus, error) {
	return m.ts.GetTetragonStates(tetragonID)
}

// GetTetragonsForIDs gets all the tetragon infos for the given ids.
func (m *Manager) GetTetragonsForIDs(ids []uuid.UUID) ([]*storepb.TetragonInfo, error) {
	return m.ts.GetTetragonsForIDs(ids)
}

// RemoveTetragons starts the termination process for the tetragons with the given names.
func (m *Manager) RemoveTetragons(names []string) error {
	fsIDs, err := m.ts.GetTetragonsWithNames(names)
	if err != nil {
		return err
	}

	ids := make([]uuid.UUID, len(fsIDs))

	for i, id := range fsIDs {
		if id == nil {
			return fmt.Errorf("Could not find tetragon for given name: %s", names[i])
		}
		ids[i] = *id
	}

	return m.ts.DeleteTetragonTTLs(ids)
}

// DeleteAgent deletes tetragons on the given agent.
func (m *Manager) DeleteAgent(agentID uuid.UUID) error {
	return m.ts.DeleteTetragonsForAgent(agentID)
}

// Close cleans up the goroutines created and renders this no longer useable.
func (m *Manager) Close() {
	m.once.Do(func() {
		close(m.done)
	})
	m.ts = nil
	m.agtMgr = nil
}
