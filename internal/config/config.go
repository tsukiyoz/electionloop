package config

import (
	"fmt"

	"zen/cmd/options/runner"

	"github.com/google/uuid"
)

type Config struct {
	ETCD   *ETCDConfig
	NodeID string
}

type ETCDConfig struct {
	Endpoints []string

	TLSCacert string
	TLSCert   string
	TLSKey    string
}

func NewConfig(opts *runner.Options) (*Config, error) {
	if opts == nil {
		return nil, fmt.Errorf("cliopts is not set")
	}

	c := &Config{
		NodeID: opts.NodeID,
	}

	if opts.ETCD != nil {
		c.ETCD = &ETCDConfig{
			Endpoints: opts.ETCD.Endpoints,
			TLSCacert: opts.ETCD.TlsCacert,
			TLSCert:   opts.ETCD.TlsCert,
			TLSKey:    opts.ETCD.TlsKey,
		}
	}

	if c.NodeID == "" {
		c.NodeID = uuid.NewString()
	}

	return c, nil
}
