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

package file_source_test

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
	"px.dev/pixie/src/carnot/planner/file_source/ir"
	"px.dev/pixie/src/common/base/statuspb"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/messages/messagespb"
	mock_agent "px.dev/pixie/src/vizier/services/metadata/controllers/agent/mock"
	"px.dev/pixie/src/vizier/services/metadata/controllers/file_source"
	mock_file_source "px.dev/pixie/src/vizier/services/metadata/controllers/file_source/mock"
	"px.dev/pixie/src/vizier/services/metadata/storepb"
	"px.dev/pixie/src/vizier/services/shared/agentpb"
)

func TestCreateFileSource(t *testing.T) {
	tests := []struct {
		name                    string
		originalFileSource      *ir.FileSourceDeployment
		originalFileSourceState statuspb.LifeCycleState
		newFileSource           *ir.FileSourceDeployment
		expectError             bool
		expectOldUpdated        bool
		expectTTLUpdateOnly     bool
	}{
		{
			name:               "test_file_source",
			originalFileSource: nil,
			newFileSource: &ir.FileSourceDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			expectError: false,
		},
		{
			name: "existing file source match",
			originalFileSource: &ir.FileSourceDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			originalFileSourceState: statuspb.RUNNING_STATE,
			newFileSource: &ir.FileSourceDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			expectTTLUpdateOnly: true,
		},
		{
			name: "existing file source, not exactly the same (1)",
			originalFileSource: &ir.FileSourceDeployment{
				GlobPattern: "/tmp/test",
				TableName:   "/tmp/test",
				TTL: &types.Duration{
					Seconds: 5,
				},
			},
			originalFileSourceState: statuspb.RUNNING_STATE,
			newFileSource: &ir.FileSourceDeployment{
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
			mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

			origID := uuid.Must(uuid.NewV4())

			if test.originalFileSource == nil {
				mockFileSourceStore.
					EXPECT().
					GetFileSourcesWithNames([]string{"test_file_source"}).
					Return([]*uuid.UUID{nil}, nil)
			} else {
				mockFileSourceStore.
					EXPECT().
					GetFileSourcesWithNames([]string{"test_file_source"}).
					Return([]*uuid.UUID{&origID}, nil)
				mockFileSourceStore.
					EXPECT().
					GetFileSource(origID).
					Return(&storepb.FileSourceInfo{
						ExpectedState: test.originalFileSourceState,
						FileSource:    test.originalFileSource,
					}, nil)
			}

			if test.expectTTLUpdateOnly {
				mockFileSourceStore.
					EXPECT().
					SetFileSourceTTL(origID, time.Second*5)
			}

			if test.expectOldUpdated {
				mockFileSourceStore.
					EXPECT().
					DeleteFileSourceTTLs([]uuid.UUID{origID}).
					Return(nil)
			}

			var newID uuid.UUID

			if !test.expectError && !test.expectTTLUpdateOnly {
				mockFileSourceStore.
					EXPECT().
					UpsertFileSource(gomock.Any(), gomock.Any()).
					DoAndReturn(func(id uuid.UUID, tpInfo *storepb.FileSourceInfo) error {
						newID = id
						assert.Equal(t, &storepb.FileSourceInfo{
							FileSource:    test.newFileSource,
							Name:          "test_file_source",
							ID:            utils.ProtoFromUUID(id),
							ExpectedState: statuspb.RUNNING_STATE,
						}, tpInfo)
						return nil
					})

				mockFileSourceStore.
					EXPECT().
					SetFileSourceWithName("test_file_source", gomock.Any()).
					DoAndReturn(func(name string, id uuid.UUID) error {
						assert.Equal(t, newID, id)
						return nil
					})

				mockFileSourceStore.
					EXPECT().
					SetFileSourceTTL(gomock.Any(), time.Second*5).
					DoAndReturn(func(id uuid.UUID, ttl time.Duration) error {
						assert.Equal(t, newID, id)
						return nil
					})
			}

			mockAgtMgr := mock_agent.NewMockManager(ctrl)
			fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
			defer fileSourceMgr.Close()

			actualFsID, err := fileSourceMgr.CreateFileSource("test_file_source", test.newFileSource)
			if test.expectError || test.expectTTLUpdateOnly {
				assert.Equal(t, file_source.ErrFileSourceAlreadyExists, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, &newID, actualFsID)
			}
		})
	}

}

func TestGetFileSources(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

	tID1 := uuid.Must(uuid.NewV4())
	tID2 := uuid.Must(uuid.NewV4())
	expectedFileSourceInfo := []*storepb.FileSourceInfo{
		{
			ID: utils.ProtoFromUUID(tID1),
		},
		{
			ID: utils.ProtoFromUUID(tID2),
		},
	}

	mockFileSourceStore.
		EXPECT().
		GetFileSources().
		Return(expectedFileSourceInfo, nil)

	fileSources, err := fileSourceMgr.GetAllFileSources()
	require.NoError(t, err)
	assert.Equal(t, expectedFileSourceInfo, fileSources)
}

func TestGetFileSourceInfo(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

	fsID1 := uuid.Must(uuid.NewV4())
	expectedFileSourceInfo := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(fsID1),
	}

	mockFileSourceStore.
		EXPECT().
		GetFileSource(fsID1).
		Return(expectedFileSourceInfo, nil)

	fileSources, err := fileSourceMgr.GetFileSourceInfo(fsID1)
	require.NoError(t, err)
	assert.Equal(t, expectedFileSourceInfo, fileSources)
}

func TestGetFileSourceStates(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	tID1 := uuid.Must(uuid.NewV4())
	expectedFileSourceStatus1 := &storepb.AgentFileSourceStatus{
		ID:      utils.ProtoFromUUID(tID1),
		AgentID: utils.ProtoFromUUID(agentUUID1),
		State:   statuspb.RUNNING_STATE,
	}

	agentUUID2 := uuid.Must(uuid.NewV4())
	expectedFileSourceStatus2 := &storepb.AgentFileSourceStatus{
		ID:      utils.ProtoFromUUID(tID1),
		AgentID: utils.ProtoFromUUID(agentUUID2),
		State:   statuspb.PENDING_STATE,
	}

	mockFileSourceStore.
		EXPECT().
		GetFileSourceStates(tID1).
		Return([]*storepb.AgentFileSourceStatus{expectedFileSourceStatus1, expectedFileSourceStatus2}, nil)

	fileSources, err := fileSourceMgr.GetFileSourceStates(tID1)
	require.NoError(t, err)
	assert.Equal(t, expectedFileSourceStatus1, fileSources[0])
	assert.Equal(t, expectedFileSourceStatus2, fileSources[1])
}

func TestRegisterFileSource(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

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

	fileSourceID := uuid.Must(uuid.NewV4())
	fileSourceDeployment := &ir.FileSourceDeployment{}
	expectedFileSourceReq := messagespb.VizierMessage{
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
	// Serialize file source request proto into byte slice to compare with the actual message sent to agents.
	msg1, err := expectedFileSourceReq.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	mockAgtMgr.
		EXPECT().
		MessageAgents([]uuid.UUID{agentUUID1, agentUUID2}, msg1).
		Return(nil)

	err = fileSourceMgr.RegisterFileSource(mockAgents, fileSourceID, fileSourceDeployment)
	require.NoError(t, err)
}

func TestUpdateAgentFileSourceStatus(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	fsID := uuid.Must(uuid.NewV4())
	expectedFileSourceState := &storepb.AgentFileSourceStatus{
		ID:      utils.ProtoFromUUID(fsID),
		AgentID: utils.ProtoFromUUID(agentUUID1),
		State:   statuspb.RUNNING_STATE,
	}

	mockFileSourceStore.
		EXPECT().
		UpdateFileSourceState(expectedFileSourceState).
		Return(nil)

	err := fileSourceMgr.UpdateAgentFileSourceStatus(utils.ProtoFromUUID(fsID), utils.ProtoFromUUID(agentUUID1), statuspb.RUNNING_STATE, nil)
	require.NoError(t, err)
}

func TestUpdateAgentFileSourceStatus_Terminated(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

}

func TestTTLExpiration(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 5*time.Second)
	defer fileSourceMgr.Close()

	agentUUID1 := uuid.Must(uuid.NewV4())
	fsID := uuid.Must(uuid.NewV4())
	agentUUID2 := uuid.Must(uuid.NewV4())

	mockFileSourceStore.
		EXPECT().
		GetFileSourceStates(fsID).
		Return([]*storepb.AgentFileSourceStatus{
			{AgentID: utils.ProtoFromUUID(agentUUID1), State: statuspb.TERMINATED_STATE},
			{AgentID: utils.ProtoFromUUID(agentUUID2), State: statuspb.RUNNING_STATE},
		}, nil)

	mockFileSourceStore.
		EXPECT().
		DeleteFileSource(fsID).
		Return(nil)

	err := fileSourceMgr.UpdateAgentFileSourceStatus(utils.ProtoFromUUID(fsID), utils.ProtoFromUUID(agentUUID2), statuspb.TERMINATED_STATE, nil)
	require.NoError(t, err)
}

func TestUpdateAgentFileSourceStatus_RemoveFileSources(t *testing.T) {
	// Set up mock.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAgtMgr := mock_agent.NewMockManager(ctrl)
	mockFileSourceStore := mock_file_source.NewMockStore(ctrl)

	fsID1 := uuid.Must(uuid.NewV4())
	fsID2 := uuid.Must(uuid.NewV4())
	fsID3 := uuid.Must(uuid.NewV4())
	fsID4 := uuid.Must(uuid.NewV4())

	mockFileSourceStore.
		EXPECT().
		GetFileSources().
		Return([]*storepb.FileSourceInfo{
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

	mockFileSourceStore.
		EXPECT().
		GetFileSourceTTLs().
		Return([]uuid.UUID{
			fsID1,
			fsID3,
			fsID4,
		}, []time.Time{
			time.Now().Add(1 * time.Hour),
			time.Now().Add(-1 * time.Minute),
			time.Now().Add(-1 * time.Hour),
		}, nil)

	mockFileSourceStore.
		EXPECT().
		GetFileSource(fsID2).
		Return(&storepb.FileSourceInfo{
			ID: utils.ProtoFromUUID(fsID2),
		}, nil)

	mockFileSourceStore.
		EXPECT().
		GetFileSource(fsID3).
		Return(&storepb.FileSourceInfo{
			ID: utils.ProtoFromUUID(fsID3),
		}, nil)

	mockFileSourceStore.
		EXPECT().
		UpsertFileSource(fsID2, &storepb.FileSourceInfo{ID: utils.ProtoFromUUID(fsID2), ExpectedState: statuspb.TERMINATED_STATE}).
		Return(nil)

	mockFileSourceStore.
		EXPECT().
		UpsertFileSource(fsID3, &storepb.FileSourceInfo{ID: utils.ProtoFromUUID(fsID3), ExpectedState: statuspb.TERMINATED_STATE}).
		Return(nil)

	var wg sync.WaitGroup
	wg.Add(2)

	var seenDeletions []string
	msgHandler := func(msg []byte) error {
		vzMsg := &messagespb.VizierMessage{}
		err := proto.Unmarshal(msg, vzMsg)
		require.NoError(t, err)
		req := vzMsg.GetFileSourceMessage().GetRemoveFileSourceRequest()
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

	fileSourceMgr := file_source.NewManager(mockFileSourceStore, mockAgtMgr, 25*time.Millisecond)
	defer fileSourceMgr.Close()

	wg.Wait()
	assert.Contains(t, seenDeletions, fsID2.String())
	assert.Contains(t, seenDeletions, fsID3.String())
}
