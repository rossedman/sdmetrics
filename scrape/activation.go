package scrape

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
)

// DefaultManagerSocket is the service manager's Varlink socket, asked
// which unit owns a socket-activated source.
const DefaultManagerSocket = "/run/systemd/io.systemd.Manager"

// How long the socket index is trusted, and how soon a source missing
// from it may trigger a refresh. Listing all units is about a megabyte on
// a typical host, so it is not done on every scrape.
const (
	socketIndexTTL     = 5 * time.Minute
	socketIndexRefresh = time.Minute
)

// socketOwner is what a .socket unit says about one listening address.
type socketOwner struct {
	socket   string // the .socket unit
	triggers string // the service it activates, unless Accept=yes
}

// socketIndex maps listening addresses to socket units, from
// io.systemd.Unit.List. systemd v262 and newer include each socket unit's
// Listen= addresses there; on older releases the index stays empty.
type socketIndex struct {
	mu      sync.Mutex
	byAddr  map[string]socketOwner
	fetched time.Time
}

// unitListReply is the part of an io.systemd.Unit.List reply the index
// needs.
type unitListReply struct {
	Context struct {
		Type     string   `json:"Type"`
		ID       string   `json:"ID"`
		Triggers []string `json:"Triggers"`
		Socket   *struct {
			Listen []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"Listen"`
			Accept bool `json:"Accept"`
		} `json:"Socket"`
	} `json:"context"`
}

func (x *socketIndex) lookup(ctx context.Context, manager string, timeout time.Duration, paths ...string) (socketOwner, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	find := func() (socketOwner, bool) {
		for _, p := range paths {
			if o, ok := x.byAddr[p]; ok {
				return o, true
			}
		}
		return socketOwner{}, false
	}
	age := time.Since(x.fetched)
	if o, ok := find(); ok && age < socketIndexTTL {
		return o, true, nil
	}
	if x.byAddr != nil && age < socketIndexRefresh {
		o, ok := find()
		return o, ok, nil
	}
	idx, err := fetchSocketIndex(ctx, manager, timeout)
	x.fetched = time.Now()
	if err != nil {
		if x.byAddr == nil {
			x.byAddr = map[string]socketOwner{}
		}
		return socketOwner{}, false, err
	}
	x.byAddr = idx
	o, ok := find()
	return o, ok, nil
}

func fetchSocketIndex(ctx context.Context, manager string, timeout time.Duration) (map[string]socketOwner, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := varlink.Dial(ctx, manager, varlink.WithMaxFrameSize(4<<20), varlink.WithMaxFiles(0))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	idx := map[string]socketOwner{}
	for r, err := range conn.Stream(ctx, "io.systemd.Unit.List", struct{}{}) {
		if err != nil {
			return nil, fmt.Errorf("io.systemd.Unit.List: %w", err)
		}
		var u unitListReply
		if err := json.Unmarshal(r.Parameters, &u); err != nil {
			continue
		}
		c := u.Context
		if c.Type != "socket" || c.Socket == nil {
			continue
		}
		o := socketOwner{socket: c.ID}
		if !c.Socket.Accept && len(c.Triggers) > 0 {
			o.triggers = c.Triggers[0]
		}
		for _, l := range c.Socket.Listen {
			idx[l.Address] = o
		}
	}
	return idx, nil
}

// resolveActivated attributes socket-activated sources to the unit their
// socket activates (or, for Accept=yes sockets, the socket unit itself).
func (s *Scraper) resolveActivated(ctx context.Context, srcs []Source) {
	for i := range srcs {
		src := &srcs[i]
		if !errors.Is(src.UnitErr, ErrActivated) {
			continue
		}
		src.Activated = true
		paths := []string{src.Path}
		if real, err := filepath.EvalSymlinks(src.Path); err == nil && real != src.Path {
			paths = append(paths, real)
		}
		o, ok, err := s.sockets.lookup(ctx, s.opts.ManagerSocket, s.opts.Timeout, paths...)
		if err != nil {
			s.warnOnce(src.Name, "", "socket owner lookup failed", "err", err)
		}
		if !ok {
			continue
		}
		src.Unit, src.UnitErr = o.triggers, nil
		if src.Unit == "" {
			src.Unit = o.socket
		}
	}
}
