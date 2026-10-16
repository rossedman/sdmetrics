package systemdreportreceiver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/confmaptest"

	"github.com/rossedman/sdmetrics/receiver/systemdreportreceiver/internal/metadata"
)

func TestLoadConfig(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	tests := []struct {
		id      component.ID
		want    func(*Config)
		invalid bool
	}{
		{id: component.NewID(metadata.Type), want: func(*Config) {}},
		{id: component.NewIDWithName(metadata.Type, "custom"), want: func(c *Config) {
			c.CollectionInterval = 15 * time.Second
			c.Directory = "/run/user/1000/systemd/report"
			c.Include = []string{"io.systemd.*", "io.example.*"}
			c.Exclude = []string{"io.systemd.Network"}
			c.SourceTimeout = time.Second
			c.Concurrency = 4
			c.Naming = "otel"
			c.Resource = map[string]string{"deployment.environment": "prod"}
			c.ServiceNames = map[string]string{"io.example.Queue": "queue"}
			c.Units = map[string]string{"io.example.Queue": "queued.service"}
			c.SelfMetrics = false
		}},
		{id: component.NewIDWithName(metadata.Type, "badnaming"), invalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.id.String(), func(t *testing.T) {
			cfg := NewFactory().CreateDefaultConfig()
			sub, err := cm.Sub(tt.id.String())
			require.NoError(t, err)
			require.NoError(t, sub.Unmarshal(cfg))
			if tt.invalid {
				assert.Error(t, confmap.Validate(cfg))
				return
			}
			require.NoError(t, confmap.Validate(cfg))
			want := createDefaultConfig().(*Config)
			tt.want(want)
			assert.Equal(t, want, cfg)
		})
	}
}

func TestValidate(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"timeout":     func(c *Config) { c.SourceTimeout = 0 },
		"concurrency": func(c *Config) { c.Concurrency = 0 },
		"pattern":     func(c *Config) { c.Include = []string{"["} },
	} {
		c := createDefaultConfig().(*Config)
		mutate(c)
		assert.Error(t, c.Validate(), name)
	}
}
