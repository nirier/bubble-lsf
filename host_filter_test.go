package main

import "testing"

func TestHostFilters(t *testing.T) {
	h := host{name: "compute-01", cpu: "16", memory: "65536M", osrel: "7.9", queues: []string{"normal", "batch"}, status: "ok", max: "32"}
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", true},
		{"cpu>=16 memory>=64G osrel:7 queue:normal", true},
		{"CPU=16 MEMORY=0.0625T QUEUE=batch", true},
		{"memory=64 memory<65G cpu<=16", true},
		{"cpu!=32 memory>32G host:compute status=ok", true},
		{"max=32", true},
		{"compute 7.9 normal", true},
		{"cpu>16", false},
		{"memory<64G", false},
		{"queue=norm", false},
		{"queue!=normal", false},
		{"osrel=7", false},
		{"osrel=7.9", true},
		{"osrel!=8.6", true},
		{"cpu>=16 memory>=128G", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			filters, err := compileHostFilter(tc.query)
			if err != nil || matchesHost(h, filters) != tc.want {
				t.Fatalf("query=%q match=%v err=%v", tc.query, matchesHost(h, filters), err)
			}
		})
	}
	for _, query := range []string{"cpu>=", "memory>=bogus", "gpu=1", "queue>normal", "cpu=-1", "memory=64Z", "cpu=16G"} {
		if _, err := compileHostFilter(query); err == nil {
			t.Errorf("accepted invalid filter %q", query)
		}
	}
	filters, _ := compileHostFilter("cpu!=16 memory<64G")
	if matchesHost(host{cpu: "-", memory: "-"}, filters) {
		t.Fatal("unknown values matched numerical conditions")
	}
}

func TestMemoryUnits(t *testing.T) {
	for _, raw := range []string{"64G", "64GB", "64GiB", "65536m", "67108864K", "0.0625T", "64", "68719476736B"} {
		got, ok := memoryBytes(raw)
		if !ok || got != 64*1024*1024*1024 {
			t.Errorf("memory %q = %v, valid=%v", raw, got, ok)
		}
	}
}
