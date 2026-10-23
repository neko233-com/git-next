// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"
)

type gitProcess struct {
	PID, Parent uint32
	Name, Path  string
	Started     time.Time
}

func init() {
	register("git-process-watchdog", "Report or stop excess Git processes and orphan daemons", "limit interval min-age apply? watch? once? daemons?", watchdog)
}
func watchdog(a *app, o options) error {
	limit, e := o.integer("limit", 64)
	if e != nil || limit < 1 {
		return fmt.Errorf("limit must be positive")
	}
	var interval, age float64
	if _, e = fmt.Sscan(o.value("interval", "5"), &interval); e != nil {
		return e
	}
	if _, e = fmt.Sscan(o.value("min-age", "120"), &age); e != nil {
		return e
	}
	if age < 0 {
		return fmt.Errorf("min-age must not be negative")
	}
	sweep := func() (record, error) {
		running, e := gitFamily()
		if e != nil {
			return nil, e
		}
		var candidates []gitProcess
		excess := len(running) - limit
		if excess < 0 {
			excess = 0
		}
		if o.yes("daemons") {
			live := map[uint32]bool{}
			for _, p := range running {
				live[p.PID] = true
			}
			for _, p := range running {
				if p.Parent == 0 || p.Parent == 4 || live[p.Parent] || processAlive(p.Parent) || p.Started.IsZero() {
					continue
				}
				if time.Since(p.Started).Seconds() >= age {
					candidates = append(candidates, p)
				}
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].Started.Before(candidates[j].Started) })
		} else {
			sort.Slice(running, func(i, j int) bool { return running[i].Started.After(running[j].Started) })
			candidates = running[:excess]
		}
		terminated := []uint32{}
		rows := []record{}
		for _, p := range candidates {
			rows = append(rows, record{"pid": p.PID, "path": p.Path, "age": int(time.Since(p.Started).Seconds())})
			if o.yes("apply") {
				if e = terminatePID(p.PID); e == nil {
					terminated = append(terminated, p.PID)
				}
			}
		}
		r := record{"limit": limit, "live": len(running), "excess": excess, "applied": o.yes("apply"), "terminated": terminated, "candidates": rows}
		if o.yes("daemons") {
			r["min_age"] = age
			r["orphans"] = len(candidates)
		}
		return r, nil
	}
	if o.yes("watch") && !o.yes("once") && !o.yes("daemons") {
		if interval < 0.5 {
			interval = 0.5
		}
		ticker := time.NewTicker(time.Duration(interval * float64(time.Second)))
		defer ticker.Stop()
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		defer signal.Stop(stop)
		for {
			r, e := sweep()
			if e != nil {
				return e
			}
			if r["excess"].(int) > 0 {
				if e = a.json(r); e != nil {
					return e
				}
			}
			select {
			case <-ticker.C:
			case <-stop:
				return nil
			}
		}
	}
	r, e := sweep()
	if e != nil {
		return e
	}
	if e = a.json(r); e != nil {
		return e
	}
	count := r["excess"].(int)
	if o.yes("daemons") {
		count = r["orphans"].(int)
	}
	if count > 0 {
		return fmt.Errorf("%d processes required attention", count)
	}
	return nil
}
