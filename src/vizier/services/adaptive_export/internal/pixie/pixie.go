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

package pixie

import (
	"context"
	"crypto/tls"
	"fmt"
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

type Client struct {
	cloudAddr string
	ctx       context.Context

	grpcConn     *grpc.ClientConn
	pluginClient cloudpb.PluginServiceClient
}

func NewClient(ctx context.Context, apiKey string, cloudAddr string) (*Client, error) {
	if apiKey == "" {
		fmt.Println("WARNING: API key is empty!")
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
	isInternal := strings.ContainsAny(c.cloudAddr, "cluster.local")

	tlsConfig := &tls.Config{InsecureSkipVerify: isInternal}
	creds := credentials.NewTLS(tlsConfig)

	conn, err := grpc.Dial(c.cloudAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}

	c.grpcConn = conn
	c.pluginClient = cloudpb.NewPluginServiceClient(conn)
	return nil
}

func (c *Client) GetClickHousePlugin() (*cloudpb.Plugin, error) {
	req := &cloudpb.GetPluginsRequest{
		Kind: cloudpb.PK_RETENTION,
	}
	resp, err := c.pluginClient.GetPlugins(c.ctx, req)
	if err != nil {
		return nil, err
	}
	for _, plugin := range resp.Plugins {
		if plugin.Id == clickhousePluginID {
			return plugin, nil
		}
	}
	return nil, fmt.Errorf("the %s plugin could not be found", clickhousePluginID)
}

type ClickHousePluginConfig struct {
	ExportURL string
}

func (c *Client) GetClickHousePluginConfig() (*ClickHousePluginConfig, error) {
	req := &cloudpb.GetOrgRetentionPluginConfigRequest{
		PluginId: clickhousePluginID,
	}
	resp, err := c.pluginClient.GetOrgRetentionPluginConfig(c.ctx, req)
	if err != nil {
		return nil, err
	}
	exportURL := resp.CustomExportUrl
	if exportURL == "" {
		exportURL, err = c.getDefaultClickHouseExportURL()
		if err != nil {
			return nil, err
		}
	}
	return &ClickHousePluginConfig{
		ExportURL: exportURL,
	}, nil
}

func (c *Client) getDefaultClickHouseExportURL() (string, error) {
	req := &cloudpb.GetRetentionPluginInfoRequest{
		PluginId: clickhousePluginID,
	}
	info, err := c.pluginClient.GetRetentionPluginInfo(c.ctx, req)
	if err != nil {
		return "", err
	}
	return info.DefaultExportURL, nil
}

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

// DisableClickHousePlugin flips the retention plugin off without touching scripts.
// Scripts are expected to be removed separately via DeleteDataRetentionScript.
func (c *Client) DisableClickHousePlugin(version string) error {
	req := &cloudpb.UpdateRetentionPluginConfigRequest{
		PluginId: clickhousePluginID,
		Enabled:  &types.BoolValue{Value: false},
		Version:  &types.StringValue{Value: version},
	}
	_, err := c.pluginClient.UpdateRetentionPluginConfig(c.ctx, req)
	return err
}

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

func (c *Client) GetClusterScripts(clusterID, clusterName string) ([]*script.Script, error) {
	resp, err := c.pluginClient.GetRetentionScripts(c.ctx, &cloudpb.GetRetentionScriptsRequest{})
	if err != nil {
		return nil, err
	}
	var l []*script.Script
	for _, s := range resp.Scripts {
		if s.PluginId == clickhousePluginID {
			sd, err := c.getScriptDefinition(s)
			if err != nil {
				return nil, err
			}
			l = append(l, &script.Script{
				ScriptDefinition: *sd,
				ScriptId:         utils.ProtoToUUIDStr(s.ScriptID),
				ClusterIds:       getClusterIDsAsString(s.ClusterIDs),
			})
		}
	}
	return l, nil
}

func getClusterIDsAsString(clusterIDs []*uuidpb.UUID) string {
	scriptClusterID := ""
	for i, id := range clusterIDs {
		if i > 0 {
			scriptClusterID = scriptClusterID + ","
		}
		scriptClusterID = scriptClusterID + utils.ProtoToUUIDStr(id)
	}
	return scriptClusterID
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

func (c *Client) UpdateDataRetentionScript(clusterID string, scriptID string, scriptName string, description string, frequencyS int64, contents string) error {
	req := &cloudpb.UpdateRetentionScriptRequest{
		ID:          utils.ProtoFromUUIDStrOrNil(scriptID),
		ScriptName:  &types.StringValue{Value: scriptName},
		Description: &types.StringValue{Value: description},
		Enabled:     &types.BoolValue{Value: true},
		FrequencyS:  &types.Int64Value{Value: frequencyS},
		Contents:    &types.StringValue{Value: contents},
		ClusterIDs:  []*uuidpb.UUID{utils.ProtoFromUUIDStrOrNil(clusterID)},
	}
	_, err := c.pluginClient.UpdateRetentionScript(c.ctx, req)
	return err
}

func (c *Client) DeleteDataRetentionScript(scriptID string) error {
	req := &cloudpb.DeleteRetentionScriptRequest{
		ID: utils.ProtoFromUUIDStrOrNil(scriptID),
	}
	_, err := c.pluginClient.DeleteRetentionScript(c.ctx, req)
	return err
}

// SetScriptEnabled toggles a retention script's cron schedule on/off without
// tearing down the script definition. Preferred over AddDataRetentionScript +
// DeleteDataRetentionScript cycling in the adaptive reconcile loop — avoids
// churning Pixie cloud's retention plugin state every time the quiet streak
// flips.
func (c *Client) SetScriptEnabled(clusterID, scriptID, scriptName, description string, frequencyS int64, contents string, enabled bool) error {
	req := &cloudpb.UpdateRetentionScriptRequest{
		ID:          utils.ProtoFromUUIDStrOrNil(scriptID),
		ScriptName:  &types.StringValue{Value: scriptName},
		Description: &types.StringValue{Value: description},
		Enabled:     &types.BoolValue{Value: enabled},
		FrequencyS:  &types.Int64Value{Value: frequencyS},
		Contents:    &types.StringValue{Value: contents},
		ClusterIDs:  []*uuidpb.UUID{utils.ProtoFromUUIDStrOrNil(clusterID)},
	}
	_, err := c.pluginClient.UpdateRetentionScript(c.ctx, req)
	return err
}
