// Copyright 2018- The Pixie Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package pixie is a thin gRPC wrapper around Pixie cloud's
// PluginService — used by adaptive_export at boot only, to ensure the
// ClickHouse retention plugin is enabled. Retention scripts themselves
// (the PxL that Pixie runs to populate forensic_db.<pixie_table>) are
// user-defined via the Pixie UI; this package does NOT manage them.
package pixie

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"

	"github.com/gogo/protobuf/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"px.dev/pixie/src/api/go/pxapi/utils"
	"px.dev/pixie/src/api/proto/cloudpb"
	"px.dev/pixie/src/api/proto/uuidpb"
	"px.dev/pixie/src/vizier/services/adaptive_export/internal/script"
)

const (
	clickhousePluginID = "clickhouse"
	exportURLConfig    = "exportURL"
)

// Client wraps a gRPC connection to Pixie cloud's PluginService.
type Client struct {
	cloudAddr string
	ctx       context.Context

	grpcConn     *grpc.ClientConn
	pluginClient cloudpb.PluginServiceClient
}

// NewClient dials the Pixie cloud and authenticates with apiKey via
// the per-call metadata header.
func NewClient(ctx context.Context, apiKey string, cloudAddr string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("pixie: empty API key")
	}
	c := &Client{
		cloudAddr: cloudAddr,
		ctx:       metadata.AppendToOutgoingContext(ctx, "pixie-api-key", apiKey),
	}
	if err := c.init(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) init() error {
	host := c.cloudAddr
	if h, _, err := net.SplitHostPort(c.cloudAddr); err == nil {
		host = h
	}
	isInternal := host == "cluster.local" || strings.HasSuffix(host, ".cluster.local")
	tlsConfig := &tls.Config{
		InsecureSkipVerify: isInternal, //nolint:gosec // in-cluster vizier traffic only
		MinVersion:         tls.VersionTLS12,
	}
	creds := credentials.NewTLS(tlsConfig)
	conn, err := grpc.Dial(c.cloudAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	c.grpcConn = conn
	c.pluginClient = cloudpb.NewPluginServiceClient(conn)
	return nil
}

// ClickHousePluginConfig is the minimal config the ensure-on path needs.
type ClickHousePluginConfig struct {
	ExportURL string
}

// GetClickHousePlugin returns the ClickHouse retention plugin descriptor,
// or an error if it is not registered with the cloud.
func (c *Client) GetClickHousePlugin() (*cloudpb.Plugin, error) {
	req := &cloudpb.GetPluginsRequest{Kind: cloudpb.PK_RETENTION}
	resp, err := c.pluginClient.GetPlugins(c.ctx, req)
	if err != nil {
		return nil, err
	}
	for _, plugin := range resp.Plugins {
		if plugin.Id == clickhousePluginID {
			return plugin, nil
		}
	}
	return nil, fmt.Errorf("pixie: %s plugin not found", clickhousePluginID)
}

// GetClickHousePluginConfig returns the current org-level config (the
// ExportURL the retention plugin is currently writing to), falling back
// to the plugin's default if no custom URL is set.
func (c *Client) GetClickHousePluginConfig() (*ClickHousePluginConfig, error) {
	req := &cloudpb.GetOrgRetentionPluginConfigRequest{PluginId: clickhousePluginID}
	resp, err := c.pluginClient.GetOrgRetentionPluginConfig(c.ctx, req)
	if err != nil {
		return nil, err
	}
	exportURL := resp.CustomExportUrl
	if exportURL == "" {
		info, err := c.pluginClient.GetRetentionPluginInfo(c.ctx,
			&cloudpb.GetRetentionPluginInfoRequest{PluginId: clickhousePluginID})
		if err != nil {
			return nil, err
		}
		exportURL = info.DefaultExportURL
	}
	return &ClickHousePluginConfig{ExportURL: exportURL}, nil
}

// EnableClickHousePlugin turns the plugin on with the supplied
// ExportURL. Idempotent on the cloud side: calling Enable when already
// enabled re-applies the same config without effect. DisablePresets is
// true so existing user-defined retention scripts (the source of truth
// for what gets written) are not overwritten by Pixie's preset set.
func (c *Client) EnableClickHousePlugin(config *ClickHousePluginConfig, version string) error {
	req := &cloudpb.UpdateRetentionPluginConfigRequest{
		PluginId: clickhousePluginID,
		Configs: map[string]string{
			exportURLConfig: config.ExportURL,
		},
		Enabled:         &types.BoolValue{Value: true},
		Version:         &types.StringValue{Value: version},
		CustomExportUrl: &types.StringValue{Value: config.ExportURL},
		InsecureTLS:     &types.BoolValue{Value: false},
		DisablePresets:  &types.BoolValue{Value: true},
	}
	_, err := c.pluginClient.UpdateRetentionPluginConfig(c.ctx, req)
	return err
}

// GetPresetScripts returns the ClickHouse-plugin preset retention scripts.
// These are the canonical http_events / dns_events / … bulk-write PxL
// scripts the plugin ships with. INSTALL_PRESET_SCRIPTS=true on the
// adaptive_export operator boot path uses this to bootstrap a cluster
// that has no user-defined retention scripts yet (DEMO PATH).
func (c *Client) GetPresetScripts() ([]*script.ScriptDefinition, error) {
	resp, err := c.pluginClient.GetRetentionScripts(c.ctx, &cloudpb.GetRetentionScriptsRequest{})
	if err != nil {
		return nil, err
	}
	var l []*script.ScriptDefinition
	for _, s := range resp.Scripts {
		if s.PluginId == clickhousePluginID && s.IsPreset {
			sd, err := c.getScriptDefinition(s)
			if err != nil {
				return nil, err
			}
			l = append(l, sd)
		}
	}
	return l, nil
}

// GetClusterScripts returns the retention scripts CURRENTLY installed on
// clusterID. Caller diffs against GetPresetScripts to figure out what
// to add / update / delete. Filters the cloud-returned ALL-clusters
// script list to those that actually target the caller's clusterID —
// without that filter, the diff later treats other clusters' scripts
// as "stale on this cluster" and tries to delete them.
func (c *Client) GetClusterScripts(clusterID, clusterName string) ([]*script.Script, error) {
	resp, err := c.pluginClient.GetRetentionScripts(c.ctx, &cloudpb.GetRetentionScriptsRequest{})
	if err != nil {
		return nil, err
	}
	var l []*script.Script
	for _, s := range resp.Scripts {
		if s.PluginId == clickhousePluginID {
			clusterIDs := make([]string, 0, len(s.ClusterIDs))
			// Empty clusterID = no filter (legacy callers; rare).
			match := clusterID == ""
			for _, id := range s.ClusterIDs {
				idStr := utils.ProtoToUUIDStr(id)
				clusterIDs = append(clusterIDs, idStr)
				if idStr == clusterID {
					match = true
				}
			}
			if !match {
				continue
			}
			sd, err := c.getScriptDefinition(s)
			if err != nil {
				return nil, err
			}
			l = append(l, &script.Script{
				ScriptDefinition: *sd,
				ScriptId:         utils.ProtoToUUIDStr(s.ScriptID),
				ClusterIds:       strings.Join(clusterIDs, ","),
			})
		}
	}
	return l, nil
}

func (c *Client) getScriptDefinition(s *cloudpb.RetentionScript) (*script.ScriptDefinition, error) {
	resp, err := c.pluginClient.GetRetentionScript(c.ctx, &cloudpb.GetRetentionScriptRequest{ID: s.ScriptID})
	if err != nil {
		return nil, err
	}
	return &script.ScriptDefinition{
		Name:        s.ScriptName,
		Description: s.Description,
		FrequencyS:  s.FrequencyS,
		Script:      resp.Contents,
		IsPreset:    s.IsPreset,
	}, nil
}

// DeleteDataRetentionScript removes the script with the given UUID.
// Used by INSTALL_PRESET_SCRIPTS to purge stale scripts that target
// tables no longer in the schema.
func (c *Client) DeleteDataRetentionScript(scriptID string) error {
	req := &cloudpb.DeleteRetentionScriptRequest{
		ID: utils.ProtoFromUUIDStrOrNil(scriptID),
	}
	_, err := c.pluginClient.DeleteRetentionScript(c.ctx, req)
	return err
}

// AddDataRetentionScript creates a new retention script on clusterID,
// running every frequencyS seconds with the given PxL contents.
func (c *Client) AddDataRetentionScript(clusterID string, scriptName string, description string, frequencyS int64, contents string) error {
	req := &cloudpb.CreateRetentionScriptRequest{
		ScriptName:  scriptName,
		Description: description,
		FrequencyS:  frequencyS,
		Contents:    contents,
		ClusterIDs:  []*uuidpb.UUID{utils.ProtoFromUUIDStrOrNil(clusterID)},
		PluginId:    clickhousePluginID,
	}
	_, err := c.pluginClient.CreateRetentionScript(c.ctx, req)
	return err
}

// EnsureClickHousePluginEnabled is the boot-time idempotent op the
// operator calls in main.go. If the plugin is already enabled with a
// non-empty ExportURL, no-op. Otherwise, enable it with the supplied
// fallback URL. Returns the resolved ExportURL for diagnostics.
func (c *Client) EnsureClickHousePluginEnabled(fallbackExportURL string) (string, error) {
	plugin, err := c.GetClickHousePlugin()
	if err != nil {
		return "", err
	}
	if plugin.RetentionEnabled {
		cfg, err := c.GetClickHousePluginConfig()
		if err != nil {
			return "", err
		}
		if cfg.ExportURL != "" {
			return cfg.ExportURL, nil
		}
	}
	if fallbackExportURL == "" {
		return "", fmt.Errorf("pixie: plugin not enabled and no fallback ExportURL provided")
	}
	if err := c.EnableClickHousePlugin(
		&ClickHousePluginConfig{ExportURL: fallbackExportURL},
		plugin.LatestVersion,
	); err != nil {
		return "", err
	}
	return fallbackExportURL, nil
}
