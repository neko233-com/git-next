// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func (d *driver) changesForPaths(roots []string, revision string, last int64, blockValue string) ([]int64, error) {
	block := int64(1 << 20)
	if blockValue != "" {
		n, e := strconv.ParseInt(blockValue, 10, 64)
		if e != nil || n < 1 || n > 1<<20 {
			return nil, fmt.Errorf("--changes-block-size must be 1..1048576")
		}
		block = n
	}
	low, high := last+1, int64(0)
	rawRange := false
	if revision != "" && revision != "@all" {
		parts := strings.Split(strings.TrimPrefix(revision, "@"), ",")
		if len(parts) == 2 {
			if parts[0] != "" {
				n, e := strconv.ParseInt(parts[0], 10, 64)
				if e != nil {
					rawRange = true
				}
				if n > low {
					low = n
				}
			}
			if parts[1] != "#head" {
				n, e := strconv.ParseInt(parts[1], 10, 64)
				if e != nil {
					rawRange = true
				}
				high = n
			}
		}
	}
	if rawRange {
		if blockValue != "" {
			return nil, fmt.Errorf("cannot use --changes-block-size with non-numeric revisions")
		}
		seen := map[int64]bool{}
		for _, root := range roots {
			records, e := d.p4List("changes", "-s", "submitted", root+"..."+revision)
			if e != nil {
				return nil, e
			}
			for _, item := range records {
				if n := integer(item["change"]); n > last {
					seen[n] = true
				}
			}
		}
		changes := make([]int64, 0, len(seen))
		for n := range seen {
			changes = append(changes, n)
		}
		sort.Slice(changes, func(i, j int) bool { return changes[i] < changes[j] })
		return changes, nil
	}
	if high == 0 {
		item, e := d.p4Single("counter", "change")
		if e != nil {
			return nil, e
		}
		high = integer(item["value"])
	}
	if high < low {
		return nil, nil
	}
	seen := map[int64]bool{}
	for start := low; start <= high; {
		end := start + block - 1
		if end > high {
			end = high
		}
		var records []dictionary
		var failure error
		for _, root := range roots {
			values, e := d.p4List("changes", "-s", "submitted", fmt.Sprintf("%s...@%d,%d", root, start, end))
			if e != nil {
				failure = e
				break
			}
			records = append(records, values...)
		}
		if failure != nil {
			message := strings.ToLower(failure.Error())
			limited := strings.Contains(message, "maxresults") || strings.Contains(message, "maxscanrows") || strings.Contains(message, "request too large")
			if limited && block > 1 {
				block /= 2
				continue
			}
			return nil, failure
		}
		for _, item := range records {
			change := integer(item["change"])
			if change >= low && change <= high {
				seen[change] = true
			}
		}
		start = end + 1
	}
	changes := make([]int64, 0, len(seen))
	for n := range seen {
		changes = append(changes, n)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i] < changes[j] })
	return changes, nil
}
