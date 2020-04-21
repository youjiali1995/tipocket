// Copyright 2020 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"

	// use mysql
	_ "github.com/go-sql-driver/mysql"

	"github.com/pingcap/tipocket/pkg/test-infra/tidb"

	"github.com/pingcap/tipocket/cmd/util"
	"github.com/pingcap/tipocket/pkg/cluster"
	"github.com/pingcap/tipocket/pkg/control"
	"github.com/pingcap/tipocket/pkg/test-infra/fixture"
	resolvelock "github.com/pingcap/tipocket/tests/resolve-lock"
)

var (
	enableGreenGC = flag.Bool("enable-green-gc", true, "whether to enable green gc")
	regionCount   = flag.Int("region-count", 200, "count of regions")
	lockPerRegion = flag.Int("lock-per-region", 10, "count of locks in each region")
	workers       = flag.Int("worker", 10, "count of workers to generate locks")
)

func main() {
	flag.Parse()
	cfg := control.Config{
		Mode:        control.ModeSelfScheduled,
		ClientCount: 1,
		RunTime:     fixture.Context.RunTime,
		RunRound:    1,
	}
	suit := util.Suit{
		Config: &cfg,
		// Provisioner: cluster.NewK8sProvisioner(),
		Provisioner: cluster.NewLocalClusterProvisioner([]string{"127.0.0.1:4000"}, []string{"127.0.0.1:2379"}, []string{"127.0.0.1:20171", "127.0.0.1:20172", "127.0.0.1:20173", "127.0.0.1:20174"}),
		ClientCreator: resolvelock.CaseCreator{Cfg: &resolvelock.Config{
			EnableGreenGC: *enableGreenGC,
			RegionCount:   *regionCount,
			LockPerRegion: *lockPerRegion,
			Worker:        *workers,
		}},
		NemesisGens: util.ParseNemesisGenerators(fixture.Context.Nemesis),
		ClusterDefs: tidb.RecommendedTiDBCluster(fixture.Context.Namespace, fixture.Context.Namespace, fixture.Context.ImageVersion, fixture.TiDBImageConfig{}),
	}
	suit.Run(context.Background())
}
