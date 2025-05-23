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

package tetragon_test

import (
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"px.dev/pixie/src/carnot/planner/tetragon/ir"
	"px.dev/pixie/src/common/base/statuspb"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/messages/messagespb"
	mock_agent "px.dev/pixie/src/vizier/services/metadata/controllers/agent/mock"
	"px.dev/pixie/src/vizier/services/metadata/controllers/tetragon"
	mock_tetragon "px.dev/pixie/src/vizier/services/metadata/controllers/tetragon/mock"
	"px.dev/pixie/src/vizier/services/metadata/storepb"
	"px.dev/pixie/src/vizier/services/shared/agentpb"
)

func TestCreateTetragon(t *testing.T) {
	tests := []struct {
		name                    string
		originalTetragon      *ir.TetragonDeployment
		originalTetragonState statuspb.LifeCycleState
		newTetragon           *ir.TetragonDeployment
		expectError             bool
		expectOldUpdated        bool
		expectTTLUpdateOnly     bool
	}{
		{
			name:               "test_tetragon",
			originalTetragon: nil,
			newTetragon: &ir.TetragonDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			expectError: false,
		},
		{
			name: "existing tetragon match",
			originalTetragon: &ir.TetragonDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			originalTetragonState: statuspb.RUNNING_STATE,
			newTetragon: &ir.TetragonDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			expectTTLUpdateOnly: true,
		},
		{
			name: "existing tetragon, not exactly the same (1)",
			originalTetragon: &ir.TetragonDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			originalTetragonState: statuspb.RUNNING_STATE,
			newTetragon: &ir.TetragonDeployment{
				GlobPattern: "/tmp/test.json",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			expectOldUpdated: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Set up mock.
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

			origID := uuid.Must(uuid.NewV4())

			if test.originalTetragon == nil {
				mockTetragonStore.
					EXPECT().
					GetTetragonsWithNames([]string{"test_tetragon"}).
					Return([]*uuid.UUID{nil}, nil)
			} else {
				mockTetragonStore.
					EXPECT().
					GetTetragonsWithNames([]string{"test_tetragon"}).
					Return([]*uuid.UUID{&origID}, nil)
				mockTetragonStore.
					EXPECT().
					GetTetragon(origID).
					Return(&storepb.TetragonInfo{
						ExpectedState: test.originalTetragonState,
						Tetragon:    test.originalTetragon,
					}, nil)
			}

			if test.expectTTLUpdateOnly {
				mockTetragonStore.
					EXPECT().
					SetTetragonTTL(origID, time.Second*5)
			}

			if test.expectOldUpdated {
				mockTetragonStore.
					EXPECT().
					DeleteTetragonTTLs([]uuid.UUID{origID}).
					Return(nil)
			}

			var newID uuid.UUID

			if !test.expectError && !test.expectTTLUpdateOnly {
				mockTetragonStore.
					EXPECT().
					UpsertTetragon(gomock.Any(), gomock.Any()).
					DoAndReturn(func(id uuid.UUID, tpInfo *storepb.TetragonInfo) error {
						newID = id
						assert.Equal(t, &storepb.TetragonInfo{
							Tetragon:    test.newTetragon,
							Name:          "test_tetragon",
							ID:            utils.ProtoFromUUID(id),
							ExpectedState: statuspb.RUNNING_STATE,
						}, tpInfo)
						return nil
					})

				mockTetragonStore.
					EXPECT().
					SetTetragonWithName("test_tetragon", gomock.Any()).
					DoAndReturn(func(name string, id uuid.UUID) error {
						assert.Equal(t, newID, id)
						return nil
					})

				mockTetragonStore.
					EXPECT().
					SetTetragonTTL(gomock.Any(), time.Second*5).
					DoAndReturn(func(id uuid.UUID, ttl time.Duration) error {
						assert.Equal(t, newID, id)
						return nil
					})
			}

			mockAgtMgr := mock_agent.NewMockManager(ctrl)
			TetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
			defer TetragonMgr.Close()

			actualFsID, err := TetragonMgr.CreateTetragon("test_tetragon", test.newTetragon)
			if test.expectError || test.expectTTLUpdateOnly {
				assert.Equal(t, tetragon.ErrTetragonAlreadyExists, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, &newID, actualFsID)
			}
		})
	}
}

func TestGetTetragons(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	TetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer TetragonMgr.Close()

	tID1 := uuid.Must(uuid.NewV4())
	tID2 := uuid.Must(uuid.NewV4())
	expectedTetragonInfo := []*storepb.TetragonInfo{
		{
			ID: utils.ProtoFromUUID(tID1),
		},
		{
			ID: utils.ProtoFromUUID(tID2),
		},
	}

	mockTetragonStore.
		EXPECT().
		GetTetragons().
		Return(expectedTetragonInfo, nil)

	tetragons, err := TetragonMgr.GetAllTetragons()
	require.NoError(t, err)
	assert.Equal(t, expectedTetragonInfo, tetragons)
}

func TestGetTetragonInfo(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer tetragonMgr.Close()

	fsID1 := uuid.Must(uuid.NewV4())
	expectedTetragonInfo := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(fsID1),
	}

	mockTetragonStore.
		EXPECT().
		GetTetragon(fsID1).
		Return(expectedTetragonInfo, nil)

	tetragons, err := tetragonMgr.GetTetragonInfo(fsID1)
	require.NoError(t, err)
	assert.Equal(t, expectedTetragonInfo, tetragons)
}

func TestGetTetragonStates(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer tetragonMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	tID1 := uuid.Must(uuid.NewV4())
	expectedTetragonStatus1 := &storepb.AgentTetragonStatus{
		ID:      utils.ProtoFromUUID(tID1),
		AgentID: utils.ProtoFromUUID(agentUUID1),
		State:   statuspb.RUNNING_STATE,
	}

	agentUUID2 := uuid.Must(uuid.NewV4())
	expectedTetragonStatus2 := &storepb.AgentTetragonStatus{
		ID:      utils.ProtoFromUUID(tID1),
		AgentID: utils.ProtoFromUUID(agentUUID2),
		State:   statuspb.PENDING_STATE,
	}

	mockTetragonStore.
		EXPECT().
		GetTetragonStates(tID1).
		Return([]*storepb.AgentTetragonStatus{expectedTetragonStatus1, expectedTetragonStatus2}, nil)

	tetragons, err := tetragonMgr.GetTetragonStates(tID1)
	require.NoError(t, err)
	assert.Equal(t, expectedTetragonStatus1, tetragons[0])
	assert.Equal(t, expectedTetragonStatus2, tetragons[1])
}

func TestRegisterTetragon(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer tetragonMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	agentUUID2 := uuid.Must(uuid.NewV4())
	upb1 := utils.ProtoFromUUID(agentUUID1)
	upb2 := utils.ProtoFromUUID(agentUUID2)
	mockAgents := []*agentpb.Agent{
		// Should match programUpTo5.18.0 and programFrom5.10.0To5.18.0
		{
			Info: &agentpb.AgentInfo{
				AgentID: upb1,
			},
		},
		{
			Info: &agentpb.AgentInfo{
				AgentID: upb2,
			},
		},
	}

	tetragonID := uuid.Must(uuid.NewV4())
	tetragonDeployment := &ir.TetragonDeployment{}
	expectedTetragonReq := messagespb.VizierMessage{
		Msg: &messagespb.VizierMessage_TetragonMessage{
			TetragonMessage: &messagespb.TetragonMessage{
				Msg: &messagespb.TetragonMessage_RegisterTetragonRequest{
					RegisterTetragonRequest: &messagespb.RegisterTetragonRequest{
						TetragonDeployment: tetragonDeployment,
						ID:                   utils.ProtoFromUUID(tetragonID),
					},
				},
			},
		},
	}
	// Serialize tetragon request proto into byte slice to compare with the actual message sent to agents.
	msg1, err := expectedTetragonReq.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	mockAgtMgr.
		EXPECT().
		MessageAgents([]uuid.UUID{agentUUID1, agentUUID2}, msg1).
		Return(nil)

	err = tetragonMgr.RegisterTetragon(mockAgents, tetragonID, tetragonDeployment)
	require.NoError(t, err)
}

func TestUpdateAgentTetragonStatus(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer tetragonMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	fsID := uuid.Must(uuid.NewV4())
	expectedTetragonState := &storepb.AgentTetragonStatus{
		ID:      utils.ProtoFromUUID(fsID),
		AgentID: utils.ProtoFromUUID(agentUUID1),
		State:   statuspb.RUNNING_STATE,
	}

	mockTetragonStore.
		EXPECT().
		UpdateTetragonState(expectedTetragonState).
		Return(nil)

	err := tetragonMgr.UpdateAgentTetragonStatus(utils.ProtoFromUUID(fsID), utils.ProtoFromUUID(agentUUID1), statuspb.RUNNING_STATE, nil)
	require.NoError(t, err)
}

func TestUpdateAgentTetragonStatus_Terminated(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer tetragonMgr.Close()
	agentUUID1 := uuid.Must(uuid.NewV4())
	fsID := uuid.Must(uuid.NewV4())
	agentUUID2 := uuid.Must(uuid.NewV4())

	mockTetragonStore.
		EXPECT().
		GetTetragonStates(fsID).
		Return([]*storepb.AgentTetragonStatus{
			{AgentID: utils.ProtoFromUUID(agentUUID1), State: statuspb.TERMINATED_STATE},
			{AgentID: utils.ProtoFromUUID(agentUUID2), State: statuspb.RUNNING_STATE},
		}, nil)

	mockTetragonStore.
		EXPECT().
		DeleteTetragon(fsID).
		Return(nil)

	err := tetragonMgr.UpdateAgentTetragonStatus(utils.ProtoFromUUID(fsID), utils.ProtoFromUUID(agentUUID2), statuspb.TERMINATED_STATE, nil)
	require.NoError(t, err)
}

func TestTTLExpiration(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 5*time.Second)
	defer tetragonMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	fsID := uuid.Must(uuid.NewV4())
	agentUUID2 := uuid.Must(uuid.NewV4())

	mockTetragonStore.
		EXPECT().
		GetTetragonStates(fsID).
		Return([]*storepb.AgentTetragonStatus{
			{AgentID: utils.ProtoFromUUID(agentUUID1), State: statuspb.TERMINATED_STATE},
			{AgentID: utils.ProtoFromUUID(agentUUID2), State: statuspb.RUNNING_STATE},
		}, nil)

	mockTetragonStore.
		EXPECT().
		DeleteTetragon(fsID).
		Return(nil)

	err := tetragonMgr.UpdateAgentTetragonStatus(utils.ProtoFromUUID(fsID), utils.ProtoFromUUID(agentUUID2), statuspb.TERMINATED_STATE, nil)
	require.NoError(t, err)
}

func TestUpdateAgentTetragonStatus_RemoveTetragons(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockTetragonStore := mock_tetragon.NewMockStore(ctrl)

	fsID1 := uuid.Must(uuid.NewV4())
	fsID2 := uuid.Must(uuid.NewV4())
	fsID3 := uuid.Must(uuid.NewV4())
	fsID4 := uuid.Must(uuid.NewV4())

	mockTetragonStore.
		EXPECT().
		GetTetragons().
		Return([]*storepb.TetragonInfo{
			{
				ID: utils.ProtoFromUUID(fsID1),
			},
			{
				ID: utils.ProtoFromUUID(fsID2),
			},
			{
				ID: utils.ProtoFromUUID(fsID3),
			},
			{
				ID:            utils.ProtoFromUUID(fsID4),
				ExpectedState: statuspb.TERMINATED_STATE,
			},
		}, nil)

	mockTetragonStore.
		EXPECT().
		GetTetragonTTLs().
		Return([]uuid.UUID{
			fsID1,
			fsID3,
			fsID4,
		}, []time.Time{
			time.Now().Add(1 * time.Hour),
			time.Now().Add(-1 * time.Minute),
			time.Now().Add(-1 * time.Hour),
		}, nil)

	mockTetragonStore.
		EXPECT().
		GetTetragon(fsID2).
		Return(&storepb.TetragonInfo{
			ID: utils.ProtoFromUUID(fsID2),
		}, nil)

	mockTetragonStore.
		EXPECT().
		GetTetragon(fsID3).
		Return(&storepb.TetragonInfo{
			ID: utils.ProtoFromUUID(fsID3),
		}, nil)

	mockTetragonStore.
		EXPECT().
		UpsertTetragon(fsID2, &storepb.TetragonInfo{ID: utils.ProtoFromUUID(fsID2), ExpectedState: statuspb.TERMINATED_STATE}).
		Return(nil)

	mockTetragonStore.
		EXPECT().
		UpsertTetragon(fsID3, &storepb.TetragonInfo{ID: utils.ProtoFromUUID(fsID3), ExpectedState: statuspb.TERMINATED_STATE}).
		Return(nil)

	var wg sync.WaitGroup
	wg.Add(2)

	var seenDeletions []string
	msgHandler := func(msg []byte) error {
		vzMsg := &messagespb.VizierMessage{}
		err := proto.Unmarshal(msg, vzMsg)
		require.NoError(t, err)
		req := vzMsg.GetTetragonMessage().GetRemoveTetragonRequest()
		assert.NotNil(t, req)
		seenDeletions = append(seenDeletions, utils.ProtoToUUIDStr(req.ID))

		wg.Done()
		return nil
	}

	mockAgtMgr.
		EXPECT().
		MessageActiveAgents(gomock.Any()).
		Times(2).
		DoAndReturn(msgHandler)

	tetragonMgr := tetragon.NewManager(mockTetragonStore, mockAgtMgr, 25*time.Millisecond)
	defer tetragonMgr.Close()

	wg.Wait()
	assert.Contains(t, seenDeletions, fsID2.String())
	assert.Contains(t, seenDeletions, fsID3.String())
}
