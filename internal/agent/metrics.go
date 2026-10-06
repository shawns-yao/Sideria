package agent

import (
	"context"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"runtime"
	"time"
)

type Sampler struct {
	last        time.Time
	network     map[string]gnet.IOCountersStat
	initialized bool
}

func Rate(current, previous uint64, elapsed time.Duration) *float64 {
	if elapsed <= 0 || current < previous {
		return nil
	}
	v := float64(current-previous) / elapsed.Seconds()
	return &v
}
func (s *Sampler) Sample(ctx context.Context) protocol.Snapshot {
	now := time.Now().UTC()
	v := protocol.Snapshot{SampledAt: now, Cores: runtime.NumCPU(), Disks: []protocol.Disk{}, Networks: []protocol.Network{}, Errors: []string{}}
	if a, e := cpu.PercentWithContext(ctx, 0, false); e == nil && len(a) > 0 && s.initialized {
		v.CPU = &a[0]
	} else {
		v.Errors = append(v.Errors, "cpu baseline or collection unavailable")
	}
	s.initialized = true
	if m, e := mem.VirtualMemoryWithContext(ctx); e == nil {
		v.MemoryTotal = m.Total
		v.MemoryAvailable = m.Available
		v.MemoryValid = true
	} else {
		v.Errors = append(v.Errors, "memory unavailable")
	}
	if m, e := mem.SwapMemoryWithContext(ctx); e == nil {
		v.SwapTotal = m.Total
		v.SwapUsed = m.Used
	}
	if l, e := load.AvgWithContext(ctx); e == nil {
		v.Load = []float64{l.Load1, l.Load5, l.Load15}
	}
	if h, e := host.InfoWithContext(ctx); e == nil {
		v.Uptime = h.Uptime
		v.OS = h.Platform + " " + h.PlatformVersion + " / " + h.KernelArch
		v.Hostname = h.Hostname
	}
	parts, e := disk.PartitionsWithContext(ctx, false)
	if e != nil {
		v.Errors = append(v.Errors, "mounts unavailable")
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Mountpoint] || len(v.Disks) >= 64 {
			continue
		}
		seen[p.Mountpoint] = true
		if d, e := disk.UsageWithContext(ctx, p.Mountpoint); e == nil {
			v.Disks = append(v.Disks, protocol.Disk{Mount: p.Mountpoint, Total: d.Total, Used: d.Used, InodesTotal: d.InodesTotal, InodesUsed: d.InodesUsed})
		}
	}
	if len(v.Disks) == 0 {
		if d, e := disk.UsageWithContext(ctx, "/"); e == nil {
			v.Disks = append(v.Disks, protocol.Disk{Mount: "/", Total: d.Total, Used: d.Used, InodesTotal: d.InodesTotal, InodesUsed: d.InodesUsed})
		}
	}
	counters, e := gnet.IOCountersWithContext(ctx, true)
	next := map[string]gnet.IOCountersStat{}
	if e != nil {
		v.Errors = append(v.Errors, "network unavailable")
	}
	for _, n := range counters {
		if len(v.Networks) >= 64 {
			break
		}
		x := protocol.Network{Name: n.Name, Received: n.BytesRecv, Sent: n.BytesSent}
		if old, ok := s.network[n.Name]; ok {
			x.RX = Rate(n.BytesRecv, old.BytesRecv, now.Sub(s.last))
			x.TX = Rate(n.BytesSent, old.BytesSent, now.Sub(s.last))
		}
		v.Networks = append(v.Networks, x)
		next[n.Name] = n
	}
	s.network = next
	s.last = now
	return v
}
