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
	"path"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/gogo/protobuf/proto"
	"golang.org/x/sync/errgroup"

	"px.dev/pixie/src/api/proto/uuidpb"
	"px.dev/pixie/src/utils"
	"px.dev/pixie/src/vizier/services/metadata/storepb"
	"px.dev/pixie/src/vizier/utils/datastore"
)

const (
	tetragonsPrefix      = "/tetragon/"
	tetragonStatesPrefix = "/tetragonStates/"
	tetragonTTLsPrefix   = "/tetragonTTL/"
	tetragonNamesPrefix  = "/tetragonName/"
)

// Datastore implements the TetragonStore interface on a given Datastore.
type Datastore struct {
	ds datastore.MultiGetterSetterDeleterCloser
}

// NewDatastore wraps the datastore in a file source store
func NewDatastore(ds datastore.MultiGetterSetterDeleterCloser) *Datastore {
	return &Datastore{ds: ds}
}

func getTetragonWithNameKey(tetragonName string) string {
	return path.Join(tetragonNamesPrefix, tetragonName)
}

func getTetragonKey(tetragonID uuid.UUID) string {
	return path.Join(tetragonsPrefix, tetragonID.String())
}

func getTetragonStatesKey(tetragonID uuid.UUID) string {
	return path.Join(tetragonStatesPrefix, tetragonID.String())
}

func getTetragonStateKey(tetragonID uuid.UUID, agentID uuid.UUID) string {
	return path.Join(tetragonStatesPrefix, tetragonID.String(), agentID.String())
}

func getTetragonTTLKey(tetragonID uuid.UUID) string {
	return path.Join(tetragonTTLsPrefix, tetragonID.String())
}

// GetTetragonsWithNames gets which file source  is associated with the given name.
func (t *Datastore) GetTetragonsWithNames(tetragonNames []string) ([]*uuid.UUID, error) {
	eg := errgroup.Group{}
	ids := make([]*uuid.UUID, len(tetragonNames))
	for i := 0; i < len(tetragonNames); i++ {
		i := i // Closure for goroutine
		eg.Go(func() error {
			val, err := t.ds.Get(getTetragonWithNameKey(tetragonNames[i]))
			if err != nil {
				return err
			}
			if val == nil {
				return nil
			}
			uuidPB := &uuidpb.UUID{}
			err = proto.Unmarshal(val, uuidPB)
			if err != nil {
				return err
			}
			id := utils.UUIDFromProtoOrNil(uuidPB)
			ids[i] = &id
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}

	return ids, nil
}

// SetTetragonWithName associates the file source  with the given name with the one with the provided ID.
func (t *Datastore) SetTetragonWithName(tetragonName string, tetragonID uuid.UUID) error {
	tetragonIDpb := utils.ProtoFromUUID(tetragonID)
	val, err := tetragonIDpb.Marshal()
	if err != nil {
		return err
	}

	return t.ds.Set(getTetragonWithNameKey(tetragonName), string(val))
}

// UpsertTetragon updates or creates a new file source  entry in the store.
func (t *Datastore) UpsertTetragon(tetragonID uuid.UUID, tetragonInfo *storepb.TetragonInfo) error {
	val, err := tetragonInfo.Marshal()
	if err != nil {
		return err
	}

	return t.ds.Set(getTetragonKey(tetragonID), string(val))
}

// DeleteTetragon deletes the file source  from the store.
func (t *Datastore) DeleteTetragon(tetragonID uuid.UUID) error {
	err := t.ds.DeleteAll([]string{getTetragonKey(tetragonID)})
	if err != nil {
		return err
	}

	return t.ds.DeleteWithPrefix(getTetragonStatesKey(tetragonID))
}

// GetTetragon gets the file source  info from the store, if it exists.
func (t *Datastore) GetTetragon(tetragonID uuid.UUID) (*storepb.TetragonInfo, error) {
	resp, err := t.ds.Get(getTetragonKey(tetragonID))
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}

	tetragonPb := &storepb.TetragonInfo{}
	err = proto.Unmarshal(resp, tetragonPb)
	if err != nil {
		return nil, err
	}
	return tetragonPb, nil
}

// GetTetragons gets all of the file source s in the store.
func (t *Datastore) GetTetragons() ([]*storepb.TetragonInfo, error) {
	_, vals, err := t.ds.GetWithPrefix(tetragonsPrefix)
	if err != nil {
		return nil, err
	}

	tetragons := make([]*storepb.TetragonInfo, len(vals))
	for i, val := range vals {
		pb := &storepb.TetragonInfo{}
		err := proto.Unmarshal(val, pb)
		if err != nil {
			continue
		}
		tetragons[i] = pb
	}
	return tetragons, nil
}

// GetTetragonsForIDs gets all of the file source s with the given it.ds.
func (t *Datastore) GetTetragonsForIDs(ids []uuid.UUID) ([]*storepb.TetragonInfo, error) {
	eg := errgroup.Group{}
	tetragons := make([]*storepb.TetragonInfo, len(ids))
	for i := 0; i < len(ids); i++ {
		i := i // Closure for goroutine
		eg.Go(func() error {
			val, err := t.ds.Get(getTetragonKey(ids[i]))
			if err != nil {
				return err
			}
			if val == nil {
				return nil
			}
			fs := &storepb.TetragonInfo{}
			err = proto.Unmarshal(val, fs)
			if err != nil {
				return err
			}
			tetragons[i] = fs
			return nil
		})
	}

	err := eg.Wait()
	if err != nil {
		return nil, err
	}

	return tetragons, nil
}

// UpdateTetragonState updates the agent file source  state in the store.
func (t *Datastore) UpdateTetragonState(state *storepb.AgentTetragonStatus) error {
	val, err := state.Marshal()
	if err != nil {
		return err
	}

	fsID := utils.UUIDFromProtoOrNil(state.ID)

	return t.ds.Set(getTetragonStateKey(fsID, utils.UUIDFromProtoOrNil(state.AgentID)), string(val))
}

// GetTetragonStates gets all the agentTetragon states for the given file source .
func (t *Datastore) GetTetragonStates(tetragonID uuid.UUID) ([]*storepb.AgentTetragonStatus, error) {
	_, vals, err := t.ds.GetWithPrefix(getTetragonStatesKey(tetragonID))
	if err != nil {
		return nil, err
	}

	tetragons := make([]*storepb.AgentTetragonStatus, len(vals))
	for i, val := range vals {
		pb := &storepb.AgentTetragonStatus{}
		err := proto.Unmarshal(val, pb)
		if err != nil {
			continue
		}
		tetragons[i] = pb
	}
	return tetragons, nil
}

// SetTetragonTTL creates a key in the datastore with the given TTL. This represents the amount of time
// that the given file source  should be persisted before terminating.
func (t *Datastore) SetTetragonTTL(tetragonID uuid.UUID, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	encodedExpiry, err := expiresAt.MarshalBinary()
	if err != nil {
		return err
	}
	return t.ds.SetWithTTL(getTetragonTTLKey(tetragonID), string(encodedExpiry), ttl)
}

// DeleteTetragonTTLs deletes the key in the datastore for the given file source  TTLs.
// This is done as a single transaction, so if any deletes fail, they all fail.
func (t *Datastore) DeleteTetragonTTLs(ids []uuid.UUID) error {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = getTetragonTTLKey(id)
	}

	return t.ds.DeleteAll(keys)
}

// DeleteTetragonsForAgent deletes the file source s for a given agent.
// Note this only purges the combo file source ID+agentID keys. Said
// file source s might still be valid and deployed on other agents.
func (t *Datastore) DeleteTetragonsForAgent(agentID uuid.UUID) error {
	fss, err := t.GetTetragons()
	if err != nil {
		return err
	}

	delKeys := make([]string, len(fss))
	for i, fs := range fss {
		delKeys[i] = getTetragonStateKey(utils.UUIDFromProtoOrNil(fs.ID), agentID)
	}

	return t.ds.DeleteAll(delKeys)
}

// GetTetragonTTLs gets the file source s which still have existing TTLs.
func (t *Datastore) GetTetragonTTLs() ([]uuid.UUID, []time.Time, error) {
	keys, vals, err := t.ds.GetWithPrefix(tetragonTTLsPrefix)
	if err != nil {
		return nil, nil, err
	}

	var ids []uuid.UUID
	var expirations []time.Time

	for i, k := range keys {
		keyParts := strings.Split(k, "/")
		if len(keyParts) != 3 {
			continue
		}
		id, err := uuid.FromString(keyParts[2])
		if err != nil {
			continue
		}
		var expiresAt time.Time
		err = expiresAt.UnmarshalBinary(vals[i])
		if err != nil {
			// This shouldn't happen for new keys, but we might have added TTLs
			// in the past without a value. So just pick some time sufficiently
			// in the future.
			// This value is only used to determine what file source s are expired
			// as of _NOW_ so this is "safe".
			expiresAt = time.Now().Add(30 * 24 * time.Hour)
		}
		ids = append(ids, id)
		expirations = append(expirations, expiresAt)
	}

	return ids, expirations, nil
}
