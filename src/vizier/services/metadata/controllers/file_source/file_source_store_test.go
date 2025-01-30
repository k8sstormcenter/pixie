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
	"os"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/gofrs/uuid"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"px.dev/pixie/src/api/proto/uuidpb"
	"px.dev/pixie/src/common/base/statuspb"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/services/metadata/storepb"
	"px.dev/pixie/src/vizier/utils/datastore/pebbledb"
)

func setupTest(t *testing.T) (*pebbledb.DataStore, *Datastore, func()) {
	memFS := vfs.NewMem()
	c, err := pebble.Open("test", &pebble.Options{
		FS: memFS,
	})
	if err != nil {
		t.Fatal("failed to initialize a pebbledb")
		os.Exit(1)
	}

	db := pebbledb.New(c, 3*time.Second)
	ts := NewDatastore(db)
	cleanup := func() {
		err := db.Close()
		if err != nil {
			t.Fatal("Failed to close db")
		}
	}

	return db, ts, cleanup
}

func TestFileSourceStore_UpsertFileSource(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	// Create file sources.
	s1 := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(tpID),
	}

	err := ts.UpsertFileSource(tpID, s1)
	require.NoError(t, err)

	savedFileSource, err := db.Get("/fileSource/" + tpID.String())
	require.NoError(t, err)
	savedFileSourcePb := &storepb.FileSourceInfo{}
	err = proto.Unmarshal(savedFileSource, savedFileSourcePb)
	require.NoError(t, err)
	assert.Equal(t, s1, savedFileSourcePb)
}

func TestFileSourceStore_GetFileSource(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	// Create file sources.
	s1 := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(tpID),
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	err = db.Set("/fileSource/"+tpID.String(), string(s1Text))
	require.NoError(t, err)

	fileSource, err := ts.GetFileSource(tpID)
	require.NoError(t, err)
	assert.NotNil(t, fileSource)

	assert.Equal(t, s1.ID, fileSource.ID)
}

func TestFileSourceStore_GetFileSources(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	// Create file sources.
	s1ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c8")
	s1 := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(s1ID),
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s2ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c9")
	s2 := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(s2ID),
	}
	s2Text, err := s2.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	err = db.Set("/fileSource/"+s1ID.String(), string(s1Text))
	require.NoError(t, err)
	err = db.Set("/fileSource/"+s2ID.String(), string(s2Text))
	require.NoError(t, err)

	fileSources, err := ts.GetFileSources()
	require.NoError(t, err)
	assert.Equal(t, 2, len(fileSources))

	ids := make([]string, len(fileSources))
	for i, tp := range fileSources {
		ids[i] = utils.ProtoToUUIDStr(tp.ID)
	}

	assert.Contains(t, ids, utils.ProtoToUUIDStr(s1.ID))
	assert.Contains(t, ids, utils.ProtoToUUIDStr(s2.ID))
}

func TestFileSourceStore_GetFileSourcesForIDs(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	// Create file sources.
	s1ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c8")
	s1 := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(s1ID),
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s2ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c9")
	s2 := &storepb.FileSourceInfo{
		ID: utils.ProtoFromUUID(s2ID),
	}
	s2Text, err := s2.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s3ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c7")

	err = db.Set("/fileSource/"+s1ID.String(), string(s1Text))
	require.NoError(t, err)
	err = db.Set("/fileSource/"+s2ID.String(), string(s2Text))
	require.NoError(t, err)

	fileSources, err := ts.GetFileSourcesForIDs([]uuid.UUID{s1ID, s2ID, s3ID})
	require.NoError(t, err)
	assert.Equal(t, 3, len(fileSources))

	ids := make([]string, len(fileSources))
	for i, tp := range fileSources {
		if tp == nil || tp.ID == nil {
			continue
		}
		ids[i] = utils.ProtoToUUIDStr(tp.ID)
	}

	assert.Contains(t, ids, utils.ProtoToUUIDStr(s1.ID))
	assert.Contains(t, ids, utils.ProtoToUUIDStr(s2.ID))
}

func TestFileSourceStore_UpdateFileSourceState(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	agentID := uuid.Must(uuid.NewV4())
	tpID := uuid.Must(uuid.NewV4())
	// Create file source state
	s1 := &storepb.AgentFileSourceStatus{
		ID:      utils.ProtoFromUUID(tpID),
		AgentID: utils.ProtoFromUUID(agentID),
		State:   statuspb.RUNNING_STATE,
	}

	err := ts.UpdateFileSourceState(s1)
	require.NoError(t, err)

	savedFileSource, err := db.Get("/fileSourceStates/" + tpID.String() + "/" + agentID.String())
	require.NoError(t, err)
	savedFileSourcePb := &storepb.AgentFileSourceStatus{}
	err = proto.Unmarshal(savedFileSource, savedFileSourcePb)
	require.NoError(t, err)
	assert.Equal(t, s1, savedFileSourcePb)
}

func TestFileSourceStore_GetFileSourceStates(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())

	agentID1 := uuid.FromStringOrNil("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	agentID2 := uuid.FromStringOrNil("6ba7b810-9dad-11d1-80b4-00c04fd430c9")

	// Create file sources.
	s1 := &storepb.AgentFileSourceStatus{
		ID:      utils.ProtoFromUUID(tpID),
		AgentID: utils.ProtoFromUUID(agentID1),
		State:   statuspb.RUNNING_STATE,
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s2 := &storepb.AgentFileSourceStatus{
		ID:      utils.ProtoFromUUID(tpID),
		AgentID: utils.ProtoFromUUID(agentID2),
		State:   statuspb.PENDING_STATE,
	}
	s2Text, err := s2.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	err = db.Set("/fileSourceStates/"+tpID.String()+"/"+agentID1.String(), string(s1Text))
	require.NoError(t, err)
	err = db.Set("/fileSourceStates/"+tpID.String()+"/"+agentID2.String(), string(s2Text))
	require.NoError(t, err)

	fileSources, err := ts.GetFileSourceStates(tpID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(fileSources))

	agentIDs := make([]string, len(fileSources))
	for i, tp := range fileSources {
		agentIDs[i] = utils.ProtoToUUIDStr(tp.AgentID)
	}

	assert.Contains(t, agentIDs, utils.ProtoToUUIDStr(s1.AgentID))
	assert.Contains(t, agentIDs, utils.ProtoToUUIDStr(s2.AgentID))
}

func TestFileSourceStore_SetFileSourceWithName(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())

	err := ts.SetFileSourceWithName("test", tpID)
	require.NoError(t, err)

	savedFileSource, err := db.Get("/fileSourceName/test")
	require.NoError(t, err)
	savedFileSourcePb := &uuidpb.UUID{}
	err = proto.Unmarshal(savedFileSource, savedFileSourcePb)
	require.NoError(t, err)
	assert.Equal(t, tpID, utils.UUIDFromProtoOrNil(savedFileSourcePb))
}

func TestFileSourceStore_GetFileSourcesWithNames(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	fileSourceIDpb := utils.ProtoFromUUID(tpID)
	val, err := fileSourceIDpb.Marshal()
	require.NoError(t, err)

	tpID2 := uuid.Must(uuid.NewV4())
	fileSourceIDpb2 := utils.ProtoFromUUID(tpID2)
	val2, err := fileSourceIDpb2.Marshal()
	require.NoError(t, err)

	err = db.Set("/fileSourceName/test", string(val))
	require.NoError(t, err)
	err = db.Set("/fileSourceName/test2", string(val2))
	require.NoError(t, err)

	fileSources, err := ts.GetFileSourcesWithNames([]string{"test", "test2"})
	require.NoError(t, err)
	assert.Equal(t, 2, len(fileSources))

	tps := make([]string, len(fileSources))
	for i, tp := range fileSources {
		tps[i] = tp.String()
	}

	assert.Contains(t, tps, tpID.String())
	assert.Contains(t, tps, tpID2.String())
}

func TestFileSourceStore_DeleteFileSource(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())

	err := db.Set("/fileSource/"+tpID.String(), "test")
	require.NoError(t, err)

	err = ts.DeleteFileSource(tpID)
	require.NoError(t, err)

	val, err := db.Get("/fileSource/" + tpID.String())
	require.NoError(t, err)
	assert.Nil(t, val)
}

func TestFileSourceStore_DeleteFileSourceTTLs(t *testing.T) {
	_, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	tpID2 := uuid.Must(uuid.NewV4())

	err := ts.DeleteFileSourceTTLs([]uuid.UUID{tpID, tpID2})
	require.NoError(t, err)
}

func TestFileSourceStore_GetFileSourceTTLs(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	// Create file sources.
	s1ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c8")
	s2ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c9")

	err := db.Set("/fileSourceTTL/"+s1ID.String(), "")
	require.NoError(t, err)
	err = db.Set("/fileSourceTTL/"+s2ID.String(), "")
	require.NoError(t, err)
	err = db.Set("/fileSourceTTL/invalid", "")
	require.NoError(t, err)

	fileSources, _, err := ts.GetFileSourceTTLs()
	require.NoError(t, err)
	assert.Equal(t, 2, len(fileSources))

	assert.Contains(t, fileSources, s1ID)
	assert.Contains(t, fileSources, s2ID)
}
