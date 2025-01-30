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
	fileSourcesPrefix      = "/fileSource/"
	fileSourceStatesPrefix = "/fileSourceStates/"
	fileSourceTTLsPrefix   = "/fileSourceTTL/"
	fileSourceNamesPrefix  = "/fileSourceName/"
)

// Datastore implements the FileSourceStore interface on a given Datastore.
type Datastore struct {
	ds datastore.MultiGetterSetterDeleterCloser
}

// NewDatastore wraps the datastore in a file source store
func NewDatastore(ds datastore.MultiGetterSetterDeleterCloser) *Datastore {
	return &Datastore{ds: ds}
}

func getFileSourceWithNameKey(fileSourceName string) string {
	return path.Join(fileSourceNamesPrefix, fileSourceName)
}

func getFileSourceKey(fileSourceID uuid.UUID) string {
	return path.Join(fileSourcesPrefix, fileSourceID.String())
}

func getFileSourceStatesKey(fileSourceID uuid.UUID) string {
	return path.Join(fileSourceStatesPrefix, fileSourceID.String())
}

func getFileSourceStateKey(fileSourceID uuid.UUID, agentID uuid.UUID) string {
	return path.Join(fileSourceStatesPrefix, fileSourceID.String(), agentID.String())
}

func getFileSourceTTLKey(fileSourceID uuid.UUID) string {
	return path.Join(fileSourceTTLsPrefix, fileSourceID.String())
}

// GetFileSourcesWithNames gets which file source  is associated with the given name.
func (t *Datastore) GetFileSourcesWithNames(fileSourceNames []string) ([]*uuid.UUID, error) {
	eg := errgroup.Group{}
	ids := make([]*uuid.UUID, len(fileSourceNames))
	for i := 0; i < len(fileSourceNames); i++ {
		i := i // Closure for goroutine
		eg.Go(func() error {
			val, err := t.ds.Get(getFileSourceWithNameKey(fileSourceNames[i]))
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

// SetFileSourceWithName associates the file source  with the given name with the one with the provided ID.
func (t *Datastore) SetFileSourceWithName(fileSourceName string, fileSourceID uuid.UUID) error {
	fileSourceIDpb := utils.ProtoFromUUID(fileSourceID)
	val, err := fileSourceIDpb.Marshal()
	if err != nil {
		return err
	}

	return t.ds.Set(getFileSourceWithNameKey(fileSourceName), string(val))
}

// UpsertFileSource updates or creates a new file source  entry in the store.
func (t *Datastore) UpsertFileSource(fileSourceID uuid.UUID, fileSourceInfo *storepb.FileSourceInfo) error {
	val, err := fileSourceInfo.Marshal()
	if err != nil {
		return err
	}

	return t.ds.Set(getFileSourceKey(fileSourceID), string(val))
}

// DeleteFileSource deletes the file source  from the store.
func (t *Datastore) DeleteFileSource(fileSourceID uuid.UUID) error {
	err := t.ds.DeleteAll([]string{getFileSourceKey(fileSourceID)})
	if err != nil {
		return err
	}

	return t.ds.DeleteWithPrefix(getFileSourceStatesKey(fileSourceID))
}

// GetFileSource gets the file source  info from the store, if it exists.
func (t *Datastore) GetFileSource(fileSourceID uuid.UUID) (*storepb.FileSourceInfo, error) {
	resp, err := t.ds.Get(getFileSourceKey(fileSourceID))
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}

	fileSourcePb := &storepb.FileSourceInfo{}
	err = proto.Unmarshal(resp, fileSourcePb)
	if err != nil {
		return nil, err
	}
	return fileSourcePb, nil
}

// GetFileSources gets all of the file source s in the store.
func (t *Datastore) GetFileSources() ([]*storepb.FileSourceInfo, error) {
	_, vals, err := t.ds.GetWithPrefix(fileSourcesPrefix)
	if err != nil {
		return nil, err
	}

	fileSources := make([]*storepb.FileSourceInfo, len(vals))
	for i, val := range vals {
		pb := &storepb.FileSourceInfo{}
		err := proto.Unmarshal(val, pb)
		if err != nil {
			continue
		}
		fileSources[i] = pb
	}
	return fileSources, nil
}

// GetFileSourcesForIDs gets all of the file source s with the given it.ds.
func (t *Datastore) GetFileSourcesForIDs(ids []uuid.UUID) ([]*storepb.FileSourceInfo, error) {
	eg := errgroup.Group{}
	fileSources := make([]*storepb.FileSourceInfo, len(ids))
	for i := 0; i < len(ids); i++ {
		i := i // Closure for goroutine
		eg.Go(func() error {
			val, err := t.ds.Get(getFileSourceKey(ids[i]))
			if err != nil {
				return err
			}
			if val == nil {
				return nil
			}
			fs := &storepb.FileSourceInfo{}
			err = proto.Unmarshal(val, fs)
			if err != nil {
				return err
			}
			fileSources[i] = fs
			return nil
		})
	}

	err := eg.Wait()
	if err != nil {
		return nil, err
	}

	return fileSources, nil
}

// UpdateFileSourceState updates the agent file source  state in the store.
func (t *Datastore) UpdateFileSourceState(state *storepb.AgentFileSourceStatus) error {
	val, err := state.Marshal()
	if err != nil {
		return err
	}

	fsID := utils.UUIDFromProtoOrNil(state.ID)

	return t.ds.Set(getFileSourceStateKey(fsID, utils.UUIDFromProtoOrNil(state.AgentID)), string(val))
}

// GetFileSourceStates gets all the agentFileSource states for the given file source .
func (t *Datastore) GetFileSourceStates(fileSourceID uuid.UUID) ([]*storepb.AgentFileSourceStatus, error) {
	_, vals, err := t.ds.GetWithPrefix(getFileSourceStatesKey(fileSourceID))
	if err != nil {
		return nil, err
	}

	fileSources := make([]*storepb.AgentFileSourceStatus, len(vals))
	for i, val := range vals {
		pb := &storepb.AgentFileSourceStatus{}
		err := proto.Unmarshal(val, pb)
		if err != nil {
			continue
		}
		fileSources[i] = pb
	}
	return fileSources, nil
}

// SetFileSourceTTL creates a key in the datastore with the given TTL. This represents the amount of time
// that the given file source  should be persisted before terminating.
func (t *Datastore) SetFileSourceTTL(fileSourceID uuid.UUID, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	encodedExpiry, err := expiresAt.MarshalBinary()
	if err != nil {
		return err
	}
	return t.ds.SetWithTTL(getFileSourceTTLKey(fileSourceID), string(encodedExpiry), ttl)
}

// DeleteFileSourceTTLs deletes the key in the datastore for the given file source  TTLs.
// This is done as a single transaction, so if any deletes fail, they all fail.
func (t *Datastore) DeleteFileSourceTTLs(ids []uuid.UUID) error {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = getFileSourceTTLKey(id)
	}

	return t.ds.DeleteAll(keys)
}

// DeleteFileSourcesForAgent deletes the file source s for a given agent.
// Note this only purges the combo file source ID+agentID keys. Said
// file source s might still be valid and deployed on other agents.
func (t *Datastore) DeleteFileSourcesForAgent(agentID uuid.UUID) error {
	fss, err := t.GetFileSources()
	if err != nil {
		return err
	}

	delKeys := make([]string, len(fss))
	for i, fs := range fss {
		delKeys[i] = getFileSourceStateKey(utils.UUIDFromProtoOrNil(fs.ID), agentID)
	}

	return t.ds.DeleteAll(delKeys)
}

// GetFileSourceTTLs gets the file source s which still have existing TTLs.
func (t *Datastore) GetFileSourceTTLs() ([]uuid.UUID, []time.Time, error) {
	keys, vals, err := t.ds.GetWithPrefix(fileSourceTTLsPrefix)
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
