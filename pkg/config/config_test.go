/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI
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
 */

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/ice/compress"
)

// isolate clears every config env var and moves into an empty working
// directory (with an empty ./conf) so only the embedded defaults and
// explicit test inputs apply.
func isolate(t *testing.T) {
	t.Helper()
	clearConfigEnv(t, reflect.TypeFor[config]())
	t.Chdir(t.TempDir())
	assert.NoError(t, os.Mkdir(filepath.Dir(configFile), 0o755))
}

func mustLoad(t *testing.T) *config {
	t.Helper()
	c := new(config)
	if !assert.NoError(t, load(c)) {
		t.FailNow()
	}
	return c
}

func TestConfig(t *testing.T) {
	isolate(t)
	t.Setenv("ZED_SERVER_MODE", "node")
	t.Setenv("ZED_NODE_ID", "8")
	t.Setenv("ZED_ETCD_ENDPOINTS", "localhost:2379")
	t.Setenv("ZED_MAX_DOCUMENT_SIZE", "1m")
	t.Setenv("ZED_WAL_SYNC_INTERVAL", "10s")

	t.Run("check", func(t *testing.T) {
		c := mustLoad(t)

		assert.Equal(t, "", c.GinMode)
		assert.Equal(t, "4080", c.ServerPort)
		assert.Equal(t, "node", c.ServerMode)
		assert.Equal(t, 8, c.NodeID)
		assert.Equal(t, "./data", c.DataPath)
		assert.Equal(t, false, c.SentryEnable)
		assert.Equal(t, "", c.SentryDSN)
		assert.Equal(t, false, c.TelemetryEnable)
		assert.Equal(t, false, c.PrometheusEnable)
		assert.Equal(t, 1000000, c.MaxDocumentSize)

		assert.Equal(t, 4096, c.BatchSize)
		assert.Equal(t, 10000, c.MaxResults)
		assert.Equal(t, 1000, c.AggregationTermsSize)

		assert.Equal(t, 10*time.Second, c.WalSyncInterval)

		assert.Equal(t, []string{"localhost:2379"}, c.Etcd.Endpoints)

		assert.Equal(t, "s3.amazonaws.com", c.S3.Endpoint)
		assert.Equal(t, "", c.S3.Bucket)
		assert.Equal(t, true, c.S3.UseSSL)

		assert.Equal(t, false, c.Plugin.GSE.Enable)
		assert.Equal(t, "small", c.Plugin.GSE.DictEmbed)
		assert.Equal(t, "./plugins/gse/dict", c.Plugin.GSE.DictPath)
	})

	t.Run("human check", func(t *testing.T) {
		tests := []struct {
			value  string
			expect int
		}{
			{value: "2048576", expect: 2048576},
			{value: "1k", expect: 1000},
			{value: "1kb", expect: 1000},
			{value: "1m", expect: 1000000},
			{value: "1mb", expect: 1000000},
			{value: "1g", expect: 1000000000},
			{value: "1gb", expect: 1000000000},
			{value: "1G", expect: 1000000000},
			{value: "1GB", expect: 1000000000},
		}
		for _, v := range tests {
			t.Setenv("ZED_MAX_DOCUMENT_SIZE", v.value)
			assert.Equal(t, v.expect, mustLoad(t).MaxDocumentSize)
		}

		dt := []struct {
			value  string
			expect time.Duration
		}{
			{value: "1", expect: time.Nanosecond},
			{value: "1ns", expect: time.Nanosecond},
			{value: "1s", expect: time.Second},
			{value: "1m", expect: time.Minute},
		}
		for _, v := range dt {
			t.Setenv("ZED_WAL_SYNC_INTERVAL", v.value)
			assert.Equal(t, v.expect, mustLoad(t).WalSyncInterval)
		}
	})
}

func TestSentryDSNOverride(t *testing.T) {
	isolate(t)
	customDSN := "https://secretToken.my.sentry.com/1234"
	t.Setenv("ZED_SENTRY_DSN", customDSN)
	assert.Equal(t, customDSN, mustLoad(t).SentryDSN)
}

func TestS3Override(t *testing.T) {
	isolate(t)
	t.Setenv("ZED_S3_ENDPOINT", "localhost:9000")
	t.Setenv("ZED_S3_BUCKET", "zed")
	t.Setenv("ZED_S3_REGION", "us-east-1")
	t.Setenv("ZED_S3_ACCESS_KEY_ID", "minioadmin")
	t.Setenv("ZED_S3_SECRET_ACCESS_KEY", "minioadmin")
	t.Setenv("ZED_S3_USE_SSL", "false")
	c := mustLoad(t)
	assert.Equal(t, "localhost:9000", c.S3.Endpoint)
	assert.Equal(t, "zed", c.S3.Bucket)
	assert.Equal(t, "us-east-1", c.S3.Region)
	assert.Equal(t, "minioadmin", c.S3.AccessKeyID)
	assert.Equal(t, "minioadmin", c.S3.SecretAccessKey)
	assert.Equal(t, false, c.S3.UseSSL)
}

func TestTOMLDefaults(t *testing.T) {
	c := new(config)
	decoder := toml.NewDecoder(bytes.NewReader(defaultConfig)).DisallowUnknownFields()
	if !assert.NoError(t, decoder.Decode(c)) {
		return
	}

	assert.Equal(t, "4080", c.ServerPort)
	assert.Equal(t, "node", c.ServerMode)
	assert.Equal(t, 1, c.NodeID)
	assert.Equal(t, "./data", c.DataPath)
	assert.Equal(t, "bolt", c.MetadataStorage)
	assert.Equal(t, "zstd", c.IceCompressor)
	assert.False(t, c.SentryEnable)
	assert.False(t, c.TelemetryEnable)
	assert.False(t, c.ProfilerEnable)
	assert.False(t, c.PrometheusEnable)
	assert.False(t, c.PrometheusAuth)
	assert.False(t, c.AccessLogEnable)
	assert.False(t, c.EnableTextKeywordMapping)
	assert.True(t, c.EnableSQLMappings)
	assert.False(t, c.AllField)
	assert.True(t, c.AllFieldPositions)
	assert.True(t, c.AutoTimestamp)
	assert.True(t, c.ExcludeSourceVectors)
	assert.Equal(t, "disk", c.VectorStorage)
	assert.Equal(t, 4096, c.BatchSize)
	assert.Equal(t, 10000, c.MaxResults)
	assert.Equal(t, 1000, c.AggregationTermsSize)
	assert.Equal(t, 1000000, c.MaxDocumentSize)
	assert.Equal(t, time.Second, c.WalSyncInterval)
	assert.False(t, c.WalRedoLogNoSync)
	assert.True(t, c.ZedSwaggerEnable)
	assert.Equal(t, "debug", c.LogLevel)
	assert.Equal(t, cluster{
		Name:                "ZedCluster",
		Partitions:          256,
		HeartbeatInterval:   5 * time.Second,
		HeartbeatTTL:        15 * time.Second,
		Replicas:            1,
		Replication:         "document",
		ReplicationInterval: 10 * time.Second,
	}, c.Cluster)
	assert.Equal(t, tier{CheckInterval: 10 * time.Minute, ColdStorageType: "s3"}, c.Tier)
	assert.Equal(t, shard{GoroutineNum: 3, Num: 3, MaxSize: 1073741824}, c.Shard)
	assert.Equal(t, logs{}, c.Logs)
	assert.Equal(t, etcd{Prefix: "/zed"}, c.Etcd)
	assert.Equal(t, gse{
		EnableStop: true,
		EnableHMM:  true,
		DictEmbed:  "small",
		DictPath:   "./plugins/gse/dict",
	}, c.Plugin.GSE)
}

func TestTOMLEnvironmentOverrides(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "unset", env: nil},
		{name: "empty", env: map[string]string{
			"ZED_SERVER_PORT": "", "ZED_WAL_SYNC_INTERVAL": "",
			"ZED_PLUGIN_GSE_ENABLE_STOP": "",
		}},
		{name: "overridden", env: map[string]string{
			"ZED_SERVER_PORT": "9090", "ZED_NODE_ID": "0",
			"ZED_SENTRY": "false", "ZED_WAL_SYNC_INTERVAL": "25ms",
			"ZED_MAX_DOCUMENT_SIZE": "2mb", "ZED_SHARD_MAX_SIZE": "2048",
			"ZED_ETCD_ENDPOINTS":           "one:2379,two:2379",
			"ZED_PLUGIN_GSE_ENABLE_STOP":   "false",
			"ZED_LOGS_INGEST_ALLOWED_UPTO": "5h",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			want := new(config)
			if !assert.NoError(t, toml.Unmarshal(defaultConfig, want)) {
				return
			}
			if tt.name == "overridden" {
				want.ServerPort = "9090"
				want.NodeID = 0
				want.SentryEnable = false
				want.WalSyncInterval = 25 * time.Millisecond
				want.MaxDocumentSize = 2000000
				want.Shard.MaxSize = 2048
				want.Etcd.Endpoints = []string{"one:2379", "two:2379"}
				want.Plugin.GSE.EnableStop = false
				want.Logs.IngestAllowedUpto = 5 * time.Hour
			}
			assert.Equal(t, want, mustLoad(t))
		})
	}
}

func clearConfigEnv(t *testing.T, rt reflect.Type) {
	t.Helper()
	for field := range rt.Fields() {
		if field.Type.Kind() == reflect.Struct {
			clearConfigEnv(t, field.Type)
		} else if tag := field.Tag.Get("toml"); tag != "" {
			assert.Equal(t, strings.ToLower(tag), tag, "toml tag must be lowercase")
			key := strings.ToUpper(tag)
			t.Setenv(key, "")
			assert.NoError(t, os.Unsetenv(key))
		}
	}
}

func TestLoadConfTOML(t *testing.T) {
	isolate(t)
	const contents = `zed_first_admin_user = "toml-admin"
zed_first_admin_password = "test-only-password"
zed_server_port = "9080"
zed_enable_sql_mappings = false
[Shard]
zed_shard_num = 5
`
	if !assert.NoError(t, os.WriteFile(configFile, []byte(contents), 0o600)) {
		return
	}
	c := mustLoad(t)
	assert.Equal(t, "toml-admin", c.FirstAdminUser)
	assert.Equal(t, "test-only-password", c.FirstAdminPassword)
	assert.Equal(t, "9080", c.ServerPort)
	assert.False(t, c.EnableSQLMappings)
	assert.Equal(t, int64(5), c.Shard.Num)
	assert.Equal(t, 3, c.Shard.GoroutineNum)
	assert.Equal(t, time.Second, c.WalSyncInterval)

	// process environment wins over the TOML file
	t.Setenv("ZED_FIRST_ADMIN_PASSWORD", "env-test-password")
	t.Setenv("ZED_ENABLE_SQL_MAPPINGS", "true")
	c = mustLoad(t)
	assert.Equal(t, "env-test-password", c.FirstAdminPassword)
	assert.True(t, c.EnableSQLMappings)
	assert.Equal(t, "9080", c.ServerPort)
}

func TestLoadIgnoresOtherSources(t *testing.T) {
	isolate(t)
	// Only ./conf/zed.toml is read; ZED_CONFIG_FILE, .env, ./zed.toml, the legacy conf/zinc.toml and default.toml are not config sources.
	assert.Equal(t, filepath.Join("conf", "zed.toml"), filepath.Clean(configFile))
	for _, name := range []string{"other.toml", "zed.toml", "default.toml", "conf/default.toml", "conf/zinc.toml"} {
		assert.NoError(t, os.WriteFile(name, []byte(`zed_server_port = "1"`+"\n"), 0o600))
	}
	assert.NoError(t, os.WriteFile(".env", []byte("ZED_SERVER_PORT=2\n"), 0o600))
	t.Setenv("ZED_CONFIG_FILE", "other.toml")
	// legacy ZINC_* environment variables are not read
	t.Setenv("ZINC_SERVER_PORT", "3")
	c := mustLoad(t)
	assert.Equal(t, "4080", c.ServerPort)
	// built-in defaults must never carry admin credentials
	assert.Empty(t, c.FirstAdminUser)
	assert.Empty(t, c.FirstAdminPassword)
}

func TestLoadInvalidTOML(t *testing.T) {
	isolate(t)
	// Go field names and legacy zinc_* keys are not accepted; only zed_* names are (matched case-insensitively by go-toml).
	tests := []struct {
		name    string
		content string
	}{
		{"syntax error", `zed_server_port = [`},
		{"unknown key", `UnknownSetting = true`},
		{"go field name", `ServerPort = "1"`},
		{"wrong type", `zed_first_admin_password = 123`},
		{"legacy zinc key", `zinc_server_port = "1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, os.WriteFile(configFile, []byte(tt.content), 0o600))
			err := load(new(config))
			if assert.ErrorContains(t, err, "invalid TOML configuration") {
				assert.NotContains(t, err.Error(), tt.content)
			}
		})
	}
	t.Run("config path is a directory", func(t *testing.T) {
		assert.NoError(t, os.Mkdir("dir", 0o755))
		t.Chdir("dir")
		assert.NoError(t, os.MkdirAll(configFile, 0o755))
		assert.ErrorContains(t, load(new(config)), "read config")
	})
}

// expectFatal runs fn with zerolog's fatal exit turned into a panic and
// reports whether log.Fatal was reached.
func expectFatal(t *testing.T, fn func()) (fatal bool) {
	t.Helper()
	prev := zerolog.FatalExitFunc
	zerolog.FatalExitFunc = func() { panic("fatal") }
	t.Cleanup(func() { zerolog.FatalExitFunc = prev })
	defer func() { fatal = recover() == "fatal" }()
	fn()
	return false
}

// keepGlobals restores the process-wide state that setup mutates.
func keepGlobals(t *testing.T) {
	t.Helper()
	cfg, algo, level := *Global, compress.Algorithm, zerolog.GlobalLevel()
	t.Cleanup(func() {
		*Global = cfg
		compress.Algorithm = algo
		zerolog.SetGlobalLevel(level)
	})
}

func TestSetup(t *testing.T) {
	tests := []struct {
		compressor string
		want       int
	}{
		{"snappy", compress.SNAPPY},
		{"S2", compress.S2},
		{"zstd", compress.ZSTD},
	}
	for _, tt := range tests {
		t.Run(tt.compressor, func(t *testing.T) {
			keepGlobals(t)
			isolate(t)
			t.Setenv("ZED_DATA_PATH", t.TempDir())
			t.Setenv("ZED_ICE_COMPRESSOR", tt.compressor)
			t.Setenv("ZED_LOG_LEVEL", "info")
			compress.Algorithm = -1
			setup()
			assert.Equal(t, tt.want, compress.Algorithm)
			assert.Equal(t, zerolog.InfoLevel, zerolog.GlobalLevel())
			assert.NoDirExists(t, filepath.Join(Global.DataPath, "_test_"))
		})
	}

	t.Run("invalid log level", func(t *testing.T) {
		keepGlobals(t)
		isolate(t)
		t.Setenv("ZED_DATA_PATH", t.TempDir())
		t.Setenv("ZED_LOG_LEVEL", "bogus")
		assert.False(t, expectFatal(t, setup))
		assert.Equal(t, "bogus", Global.LogLevel)
	})

	t.Run("invalid config file", func(t *testing.T) {
		keepGlobals(t)
		isolate(t)
		assert.NoError(t, os.WriteFile(configFile, []byte(`zed_server_port = [`), 0o600))
		assert.True(t, expectFatal(t, setup))
	})

	t.Run("data path not writable", func(t *testing.T) {
		keepGlobals(t)
		isolate(t)
		file := filepath.Join(t.TempDir(), "file")
		assert.NoError(t, os.WriteFile(file, nil, 0o600))
		t.Setenv("ZED_DATA_PATH", filepath.Join(file, "data"))
		assert.True(t, expectFatal(t, setup))
	})
}

func TestSetFieldInvalid(t *testing.T) {
	var c struct {
		Int  int
		Uint uint64
		Bool bool
	}
	rv := reflect.ValueOf(&c).Elem()

	setField(rv.Field(0), "")
	assert.Zero(t, c.Int)

	tests := []struct {
		name  string
		field int
		value string
	}{
		{"int", 0, "not-a-size"},
		{"uint", 1, "-1"},
		{"bool", 2, "maybe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ZED_TEST_SET_FIELD", tt.value)
			assert.True(t, expectFatal(t, func() { setField(rv.Field(tt.field), "ZED_TEST_SET_FIELD") }))
		})
	}
}

func TestSetupDataPathNotRemovable(t *testing.T) {
	keepGlobals(t)
	isolate(t)
	dir := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(dir, "_test_", "keep"), 0o755))
	t.Setenv("ZED_DATA_PATH", dir)
	assert.True(t, expectFatal(t, setup))
}

func TestLoadInvalidDefaults(t *testing.T) {
	isolate(t)
	prev := defaultConfig
	defaultConfig = []byte(`zed_server_port = [`)
	t.Cleanup(func() { defaultConfig = prev })
	assert.ErrorContains(t, load(new(config)), "decode built-in defaults")
}
