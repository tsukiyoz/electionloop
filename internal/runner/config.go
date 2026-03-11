package runner

import (
	"crypto/tls"
	"crypto/x509"
	"os"

	"zen/cmd/options/runner"
	options "zen/cmd/options/runner"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type Config struct {
	Options *options.Options

	NodeID     string
	EtcdConfig *clientv3.Config
}

func NewConfig(opts *runner.Options) (*Config, error) {
	c := &Config{
		Options: opts,
	}

	if opts.NodeID != "" {
		c.NodeID = opts.NodeID
	}

	etcdConfig := &clientv3.Config{
		Endpoints: opts.ETCD.Endpoints,
	}

	if opts.ETCD.TlsCacert != "" || opts.ETCD.TlsCert != "" || opts.ETCD.TlsKey != "" {
		caCert, err := os.ReadFile(opts.ETCD.TlsCacert)
		if err != nil {
			return nil, err
		}

		// Load client certificate
		cert, err := tls.LoadX509KeyPair(
			opts.ETCD.TlsCert,
			opts.ETCD.TlsKey,
		)
		if err != nil {
			return nil, err
		}

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		etcdConfig.TLS = &tls.Config{
			RootCAs:      caCertPool,
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	c.EtcdConfig = etcdConfig

	return c, nil
}

type completedConfig struct {
	*Config
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package
type CompletedConfig struct {
	*completedConfig
}

func (c *Config) Complete() (CompletedConfig, error) {
	if c.NodeID == "" {
		c.NodeID = uuid.NewString()
	}

	return CompletedConfig{&completedConfig{c}}, nil
}
