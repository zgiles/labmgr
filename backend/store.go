package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketOps    = []byte("operations")
	bucketEvents = []byte("events")
	bucketPower  = []byte("power")
)

const powerRetention = 7 * 24 * time.Hour

const maxEvents = 5000

// Store persists operations and the activity log
type Store struct {
	db  *bolt.DB
	seq atomic.Uint64
}

func OpenStore(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w (is another labmgr using it?)", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketOps, bucketEvents, bucketPower} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveOp(op *Operation) error {
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketOps).Put([]byte(op.ID), data)
	})
}

func (s *Store) DeleteOps(ids []string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOps)
		for _, id := range ids {
			if err := b.Delete([]byte(id)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) LoadOps() ([]*Operation, error) {
	var ops []*Operation
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketOps).ForEach(func(_, v []byte) error {
			var op Operation
			if err := json.Unmarshal(v, &op); err != nil {
				return nil // skip unreadable records rather than refuse to start
			}
			ops = append(ops, &op)
			return nil
		})
	})
	return ops, err
}

// AppendEvent stores an event, assigning it a time-ordered ID
func (s *Store) AppendEvent(e *Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.ID = fmt.Sprintf("%016x%04x", e.At.UnixNano(), s.seq.Add(1)&0xffff)
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketEvents)
		if err := b.Put([]byte(e.ID), data); err != nil {
			return err
		}
		// Trim the oldest once over the cap
		if n := b.Stats().KeyN; n > maxEvents+100 {
			var stale [][]byte
			c := b.Cursor()
			for k, _ := c.First(); k != nil && n-len(stale) > maxEvents; k, _ = c.Next() {
				stale = append(stale, append([]byte(nil), k...))
			}
			for _, k := range stale {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Events returns the newest events first, optionally for one server
func (s *Store) Events(serverID string, limit int) ([]Event, error) {
	events := []Event{}
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketEvents).Cursor()
		for k, v := c.Last(); k != nil && (limit <= 0 || len(events) < limit); k, v = c.Prev() {
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				continue
			}
			if serverID != "" && e.ServerID != serverID {
				continue
			}
			events = append(events, e)
		}
		return nil
	})
	return events, err
}

// PowerSample is one power draw reading: [unix milliseconds, watts]
type PowerSample [2]float64

func powerKey(serverID string, t time.Time) []byte {
	return []byte(fmt.Sprintf("%s\x00%016x", serverID, t.UnixNano()))
}

// AppendPower records a power reading
func (s *Store) AppendPower(serverID string, t time.Time, watts float64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPower).Put(powerKey(serverID, t), []byte(strconv.FormatFloat(watts, 'f', 1, 64)))
	})
}

// PowerHistory returns a server's readings since a time, oldest first, averaged
// down to at most maxPoints so long ranges stay light to draw
func (s *Store) PowerHistory(serverID string, since time.Time, maxPoints int) ([]PowerSample, error) {
	samples := []PowerSample{}
	prefix := []byte(serverID + "\x00")
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketPower).Cursor()
		for k, v := c.Seek(powerKey(serverID, since)); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			nanos, err := strconv.ParseInt(string(k[len(prefix):]), 16, 64)
			if err != nil {
				continue
			}
			watts, err := strconv.ParseFloat(string(v), 64)
			if err != nil {
				continue
			}
			samples = append(samples, PowerSample{float64(nanos / int64(time.Millisecond)), watts})
		}
		return nil
	})
	return downsample(samples, maxPoints), err
}

// downsample averages consecutive samples into at most n buckets
func downsample(samples []PowerSample, n int) []PowerSample {
	if n <= 0 || len(samples) <= n {
		return samples
	}
	out := make([]PowerSample, 0, n)
	per := float64(len(samples)) / float64(n)
	for i := 0; i < n; i++ {
		start, end := int(float64(i)*per), int(float64(i+1)*per)
		if end > len(samples) {
			end = len(samples)
		}
		if start >= end {
			continue
		}
		var t, w float64
		for _, sample := range samples[start:end] {
			t += sample[0]
			w += sample[1]
		}
		count := float64(end - start)
		out = append(out, PowerSample{math.Round(t / count), math.Round(w/count*10) / 10})
	}
	return out
}

// PrunePower drops readings older than the retention window
func (s *Store) PrunePower() error {
	cutoff := time.Now().Add(-powerRetention).UnixNano()
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPower)
		var stale [][]byte
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			i := bytes.IndexByte(k, 0)
			if i < 0 {
				continue
			}
			if nanos, err := strconv.ParseInt(string(k[i+1:]), 16, 64); err == nil && nanos < cutoff {
				stale = append(stale, append([]byte(nil), k...))
			}
		}
		for _, k := range stale {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}
