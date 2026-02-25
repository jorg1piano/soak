package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Store provides ticket persistence using NATS JetStream KV.
type Store struct {
	kv jetstream.KeyValue
	js jetstream.JetStream
	nc *nats.Conn // exported for subscribe command
	ns *server.Server
}

// NewServerStore creates a Store that starts an embedded NATS server.
func NewServerStore() (*Store, error) {
	opts := &server.Options{
		JetStream: true,
		StoreDir:  natsDataDir,
		Port:      natsPort,
		NoLog:     true,
		NoSigs:    true,
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("new nats server: %w", err)
	}
	ns.Start()

	if !ns.ReadyForConnections(100 * time.Millisecond) {
		return nil, fmt.Errorf("nats server not ready after 100ms")
	}

	os.WriteFile(portFile, []byte(fmt.Sprintf("%d", natsPort)), 0644)

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream new: %w", err)
	}

	kv, err := js.CreateOrUpdateKeyValue(context.Background(), jetstream.KeyValueConfig{
		Bucket:  "tickets",
		History: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("create kv bucket: %w", err)
	}

	return &Store{kv: kv, js: js, nc: nc, ns: ns}, nil
}

// NewClientStore creates a Store that connects to an existing NATS server.
func NewClientStore() (*Store, error) {
	url := fmt.Sprintf("nats://127.0.0.1:%d", natsPort)

	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("nats connect to %s: %w", url, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream new: %w", err)
	}

	kv, err := js.KeyValue(context.Background(), "tickets")
	if err != nil {
		return nil, fmt.Errorf("get kv bucket: %w", err)
	}

	return &Store{kv: kv, js: js, nc: nc}, nil
}

// Close closes the NATS connection and shuts down the embedded server if running.
func (s *Store) Close() {
	s.nc.Close()
	if s.ns != nil {
		s.ns.Shutdown()
		os.Remove(portFile)
	}
}

// PutTicket stores a ticket in the KV store.
func (s *Store) PutTicket(t Ticket) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = s.kv.Put(context.Background(), t.ID, data)
	return err
}

// GetTicket retrieves a ticket by ID from the KV store.
func (s *Store) GetTicket(id string) (*Ticket, error) {
	entry, err := s.kv.Get(context.Background(), id)
	if err != nil {
		return nil, err
	}
	var t Ticket
	if err := json.Unmarshal(entry.Value(), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// AllTickets retrieves all tickets from the KV store.
func (s *Store) AllTickets() ([]Ticket, error) {
	keys, err := s.kv.Keys(context.Background())
	if err != nil {
		if err == jetstream.ErrNoKeysFound {
			return nil, nil
		}
		return nil, err
	}

	var tickets []Ticket
	for _, k := range keys {
		entry, err := s.kv.Get(context.Background(), k)
		if err != nil {
			continue
		}
		var t Ticket
		if err := json.Unmarshal(entry.Value(), &t); err != nil {
			continue
		}
		tickets = append(tickets, t)
	}
	return tickets, nil
}

// DeleteTicket removes a ticket from the KV store.
func (s *Store) DeleteTicket(id string) error {
	return s.kv.Delete(context.Background(), id)
}

// Publish publishes a message to a NATS subject.
func (s *Store) Publish(subject string, data []byte) error {
	if err := s.nc.Publish(subject, data); err != nil {
		return err
	}
	if err := s.nc.Flush(); err != nil {
		return err
	}
	// Delay to ensure message is sent before connection closes
	time.Sleep(200 * time.Millisecond)
	return nil
}
