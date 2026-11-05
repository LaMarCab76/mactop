package app

import "time"

// Protected by netDiskMutex. A failed provider invalidates only its own
// baseline. Recovery and device/interface changes require a fresh baseline.
type btopRateBaselines struct {
	network map[string]NativeNetMetric
	disk    map[string]NativeDiskMetric
}

var btopRateState btopRateBaselines

func recordBtopRates(metrics NetDiskMetrics, at time.Time) {
	for key, value := range map[string]float64{"rx": metrics.InBytesPerSec, "tx": metrics.OutBytesPerSec, "disk_r": metrics.ReadKBytesPerSec * 1024, "disk_w": metrics.WriteKBytesPerSec * 1024} {
		valid := metrics.NetworkValid
		if key == "disk_r" || key == "disk_w" {
			valid = metrics.DiskValid
		}
		btopData.history(key).add(at, value, valid, "aggregate")
	}
}

func (s *btopRateBaselines) observe(network map[string]NativeNetMetric, netErr error, disk map[string]NativeDiskMetric, diskErr error, elapsed bool) (bool, bool) {
	netValid := elapsed && netErr == nil && len(network) > 0 && len(network) == len(s.network)
	if netValid {
		for name, n := range network {
			p, ok := s.network[name]
			if !ok || n.BytesRecv < p.BytesRecv || n.BytesSent < p.BytesSent || n.PacketsRecv < p.PacketsRecv || n.PacketsSent < p.PacketsSent {
				netValid = false
				break
			}
		}
	}
	diskValid := elapsed && diskErr == nil && len(disk) > 0 && len(disk) == len(s.disk)
	if diskValid {
		for name, n := range disk {
			p, ok := s.disk[name]
			if !ok || n.ReadBytes < p.ReadBytes || n.WriteBytes < p.WriteBytes || n.ReadOps < p.ReadOps || n.WriteOps < p.WriteOps {
				diskValid = false
				break
			}
		}
	}
	s.network, s.disk = network, disk
	if netErr != nil {
		s.network = nil
	}
	if diskErr != nil {
		s.disk = nil
	}
	return netValid, diskValid
}
