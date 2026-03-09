package data

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"zen/internal/config"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type Data struct {
	Etcd *clientv3.Client
}

func NewData(cfg *config.Config) (*Data, error) {
	slog.Debug("got cfg", "value", cfg)

	data := &Data{}

	if cfg.ETCD != nil {
		etcdConfig := clientv3.Config{
			Endpoints: cfg.ETCD.Endpoints,
		}

		if cfg.ETCD.TLSCacert != "" || cfg.ETCD.TLSCert != "" || cfg.ETCD.TLSKey != "" {
			caCert, err := os.ReadFile(cfg.ETCD.TLSCacert)
			if err != nil {
				return nil, err
			}

			// Load client certificate
			cert, err := tls.LoadX509KeyPair(
				cfg.ETCD.TLSCert,
				cfg.ETCD.TLSKey,
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

		client, err := clientv3.New(etcdConfig)
		if err != nil {
			return nil, err
		}

		data.Etcd = client
	}

	return data, nil
}

func (d *Data) Close() error {
	slog.Info("data closing...")
	var errs []error

	if d.Etcd != nil {
		if err := d.Etcd.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errors.Join(errs...))
	}

	return nil
}

// package main

// import (
// 	"context"
// 	"crypto/tls"
// 	"crypto/x509"
// 	"fmt"
// 	"os"

// 	"demo/app/pkg/server"

// 	clientv3 "go.etcd.io/etcd/client/v3"
// 	"go.etcd.io/etcd/client/v3/concurrency"
// )

// func main() {
// 	// Example: participate in election
// 	if err := election.Campaign(ctx, "my-node"); err != nil {
// 		panic(err)
// 	}

// 	// Watch loop
// 	watchLoop(ctx)
// }

// func watchLoop(ctx context.Context) {
// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		}
// 	}
// }
