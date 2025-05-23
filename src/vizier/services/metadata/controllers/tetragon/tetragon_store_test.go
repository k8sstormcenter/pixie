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

func TestTetragonStore_UpsertTetragon(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	// Create file sources.
	s1 := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(tpID),
	}

	err := ts.UpsertTetragon(tpID, s1)
	require.NoError(t, err)

	savedTetragon, err := db.Get("/tetragon/" + tpID.String())
	require.NoError(t, err)
	savedTetragonPb := &storepb.TetragonInfo{}
	err = proto.Unmarshal(savedTetragon, savedTetragonPb)
	require.NoError(t, err)
	assert.Equal(t, s1, savedTetragonPb)
}

func TestTetragonStore_GetTetragon(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	// Create file sources.
	s1 := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(tpID),
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	err = db.Set("/tetragon/"+tpID.String(), string(s1Text))
	require.NoError(t, err)

	Tetragon, err := ts.GetTetragon(tpID)
	require.NoError(t, err)
	assert.NotNil(t, tetragon)

	assert.Equal(t, s1.ID, tetragon.ID)
}

func TestTetragonStore_GetTetragons(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	// Create file sources.
	s1ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c8")
	s1 := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(s1ID),
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s2ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c9")
	s2 := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(s2ID),
	}
	s2Text, err := s2.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	err = db.Set("/tetragon/"+s1ID.String(), string(s1Text))
	require.NoError(t, err)
	err = db.Set("/tetragon/"+s2ID.String(), string(s2Text))
	require.NoError(t, err)

	tetragons, err := ts.GetTetragons()
	require.NoError(t, err)
	assert.Equal(t, 2, len(tetragons))

	ids := make([]string, len(tetragons))
	for i, tp := range tetragons {
		ids[i] = utils.ProtoToUUIDStr(tp.ID)
	}

	assert.Contains(t, ids, utils.ProtoToUUIDStr(s1.ID))
	assert.Contains(t, ids, utils.ProtoToUUIDStr(s2.ID))
}

func TestTetragonStore_GetTetragonsForIDs(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	// Create file sources.
	s1ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c8")
	s1 := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(s1ID),
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s2ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c9")
	s2 := &storepb.TetragonInfo{
		ID: utils.ProtoFromUUID(s2ID),
	}
	s2Text, err := s2.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s3ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c7")

	err = db.Set("/tetragon/"+s1ID.String(), string(s1Text))
	require.NoError(t, err)
	err = db.Set("/tetragon/"+s2ID.String(), string(s2Text))
	require.NoError(t, err)

	tetragons, err := ts.GetTetragonsForIDs([]uuid.UUID{s1ID, s2ID, s3ID})
	require.NoError(t, err)
	assert.Equal(t, 3, len(tetragons))

	ids := make([]string, len(tetragons))
	for i, tp := range tetragons {
		if tp == nil || tp.ID == nil {
			continue
		}
		ids[i] = utils.ProtoToUUIDStr(tp.ID)
	}

	assert.Contains(t, ids, utils.ProtoToUUIDStr(s1.ID))
	assert.Contains(t, ids, utils.ProtoToUUIDStr(s2.ID))
}

func TestTetragonStore_UpdateTetragonState(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	agentID := uuid.Must(uuid.NewV4())
	tpID := uuid.Must(uuid.NewV4())
	// Create file source state
	s1 := &storepb.AgentTetragonStatus{
		ID:      utils.ProtoFromUUID(tpID),
		AgentID: utils.ProtoFromUUID(agentID),
		State:   statuspb.RUNNING_STATE,
	}

	err := ts.UpdateTetragonState(s1)
	require.NoError(t, err)

	savedTetragon, err := db.Get("/tetragonStates/" + tpID.String() + "/" + agentID.String())
	require.NoError(t, err)
	savedTetragonPb := &storepb.AgentTetragonStatus{}
	err = proto.Unmarshal(savedTetragon, savedTetragonPb)
	require.NoError(t, err)
	assert.Equal(t, s1, savedTetragonPb)
}

func TestTetragonStore_GetTetragonStates(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())

	agentID1 := uuid.FromStringOrNil("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	agentID2 := uuid.FromStringOrNil("6ba7b810-9dad-11d1-80b4-00c04fd430c9")

	// Create file sources.
	s1 := &storepb.AgentTetragonStatus{
		ID:      utils.ProtoFromUUID(tpID),
		AgentID: utils.ProtoFromUUID(agentID1),
		State:   statuspb.RUNNING_STATE,
	}
	s1Text, err := s1.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	s2 := &storepb.AgentTetragonStatus{
		ID:      utils.ProtoFromUUID(tpID),
		AgentID: utils.ProtoFromUUID(agentID2),
		State:   statuspb.PENDING_STATE,
	}
	s2Text, err := s2.Marshal()
	if err != nil {
		t.Fatal("Unable to marshal file source pb")
	}

	err = db.Set("/tetragonStates/"+tpID.String()+"/"+agentID1.String(), string(s1Text))
	require.NoError(t, err)
	err = db.Set("/tetragonStates/"+tpID.String()+"/"+agentID2.String(), string(s2Text))
	require.NoError(t, err)

	tetragons, err := ts.GetTetragonStates(tpID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(tetragons))

	agentIDs := make([]string, len(tetragons))
	for i, tp := range tetragons {
		agentIDs[i] = utils.ProtoToUUIDStr(tp.AgentID)
	}

	assert.Contains(t, agentIDs, utils.ProtoToUUIDStr(s1.AgentID))
	assert.Contains(t, agentIDs, utils.ProtoToUUIDStr(s2.AgentID))
}

func TestTetragonStore_SetTetragonWithName(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())

	err := ts.SetTetragonWithName("test", tpID)
	require.NoError(t, err)

	savedTetragon, err := db.Get("/tetragonName/test")
	require.NoError(t, err)
	savedTetragonPb := &uuidpb.UUID{}
	err = proto.Unmarshal(savedTetragon, savedTetragonPb)
	require.NoError(t, err)
	assert.Equal(t, tpID, utils.UUIDFromProtoOrNil(savedTetragonPb))
}

func TestTetragonStore_GetTetragonsWithNames(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	TetragonIDpb := utils.ProtoFromUUID(tpID)
	val, err := tetragonIDpb.Marshal()
	require.NoError(t, err)

	tpID2 := uuid.Must(uuid.NewV4())
	tetragonIDpb2 := utils.ProtoFromUUID(tpID2)
	val2, err := tetragonIDpb2.Marshal()
	require.NoError(t, err)

	err = db.Set("/tetragonName/test", string(val))
	require.NoError(t, err)
	err = db.Set("/tetragonName/test2", string(val2))
	require.NoError(t, err)

	tetragons, err := ts.GetTetragonsWithNames([]string{"test", "test2"})
	require.NoError(t, err)
	assert.Equal(t, 2, len(tetragons))

	tps := make([]string, len(tetragons))
	for i, tp := range tetragons {
		tps[i] = tp.String()
	}

	assert.Contains(t, tps, tpID.String())
	assert.Contains(t, tps, tpID2.String())
}

func TestTetragonStore_DeleteTetragon(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())

	err := db.Set("/tetragon/"+tpID.String(), "test")
	require.NoError(t, err)

	err = ts.DeleteTetragon(tpID)
	require.NoError(t, err)

	val, err := db.Get("/tetragon/" + tpID.String())
	require.NoError(t, err)
	assert.Nil(t, val)
}

func TestTetragonStore_DeleteTetragonTTLs(t *testing.T) {
	_, ts, cleanup := setupTest(t)
	defer cleanup()

	tpID := uuid.Must(uuid.NewV4())
	tpID2 := uuid.Must(uuid.NewV4())

	err := ts.DeleteTetragonTTLs([]uuid.UUID{tpID, tpID2})
	require.NoError(t, err)
}

func TestTetragonStore_GetTetragonTTLs(t *testing.T) {
	db, ts, cleanup := setupTest(t)
	defer cleanup()

	// Create file sources.
	s1ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c8")
	s2ID := uuid.FromStringOrNil("8ba7b810-9dad-11d1-80b4-00c04fd430c9")

	err := db.Set("/tetragonTTL/"+s1ID.String(), "")
	require.NoError(t, err)
	err = db.Set("/tetragonTTL/"+s2ID.String(), "")
	require.NoError(t, err)
	err = db.Set("/tetragonTTL/invalid", "")
	require.NoError(t, err)

	tetragons, _, err := ts.GetTetragonTTLs()
	require.NoError(t, err)
	assert.Equal(t, 2, len(tetragons))

	assert.Contains(t, tetragons, s1ID)
	assert.Contains(t, tetragons, s2ID)
}
