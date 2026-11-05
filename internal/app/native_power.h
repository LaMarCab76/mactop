#ifndef MACTOP_NATIVE_POWER_H
#define MACTOP_NATIVE_POWER_H
#include <math.h>

// An observed but never-working silent counter is unknown. Once validated,
// a zero delta is real unless independent evidence identifies a gated counter.
static inline int mactop_power_counter_available(double watts, int observed,
                                                int validated, int gated) {
  return observed && !gated && isfinite(watts) && watts >= 0 && watts <= 1000 &&
         (watts > 0 || validated);
}

// Domain order: CPU, GPU, GPU SRAM, ANE, DRAM. The producer applies this
// shared decision to every Energy Model domain, including GPU and SRAM.
static inline unsigned mactop_power_domains_available(const double values[5],
    unsigned observed, unsigned validated, unsigned gated) {
  unsigned available = 0;
  for (unsigned i = 0; i < 5; i++) {
    unsigned bit = 1u << i;
    if (mactop_power_counter_available(values[i], observed & bit,
                                      validated & bit, gated & bit))
      available |= bit;
  }
  return available;
}

static inline unsigned mactop_power_gated_domains(int cpuGated, int dramGated,
    double gpuWatts, double sramWatts, double aneWatts,
    double gpuActive, int aneBusy) {
  int degraded = cpuGated || dramGated;
  // A zero with substantial GPU activity in a degraded Energy Model is
  // uncertain, even if that counter once advanced during an entitled sample.
  int gpuBusy = degraded && gpuActive > 5;
  return (cpuGated ? 1u : 0u) | (gpuBusy && gpuWatts == 0 ? 2u : 0u) |
         (gpuBusy && sramWatts == 0 ? 4u : 0u) |
         (degraded && aneBusy && aneWatts == 0 ? 8u : 0u) |
         (degraded ? 16u : 0u);
}
#endif
