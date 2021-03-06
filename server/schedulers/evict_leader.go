// Copyright 2017 PingCAP, Inc.
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

package schedulers

import (
	"fmt"
	"strconv"

	"github.com/pingcap/pd/server/core"
	"github.com/pingcap/pd/server/schedule"
	"github.com/pingcap/pd/server/schedule/filter"
	"github.com/pingcap/pd/server/schedule/operator"
	"github.com/pingcap/pd/server/schedule/opt"
	"github.com/pingcap/pd/server/schedule/selector"
	"github.com/pkg/errors"
)

func init() {
	schedule.RegisterScheduler("evict-leader", func(opController *schedule.OperatorController, args []string) (schedule.Scheduler, error) {
		if len(args) != 1 {
			return nil, errors.New("evict-leader needs 1 argument")
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		return newEvictLeaderScheduler(opController, id), nil
	})
}

type evictLeaderScheduler struct {
	*baseScheduler
	name     string
	storeID  uint64
	selector *selector.RandomSelector
}

// newEvictLeaderScheduler creates an admin scheduler that transfers all leaders
// out of a store.
func newEvictLeaderScheduler(opController *schedule.OperatorController, storeID uint64) schedule.Scheduler {
	name := fmt.Sprintf("evict-leader-scheduler-%d", storeID)
	filters := []filter.Filter{
		filter.StoreStateFilter{ActionScope: name, TransferLeader: true},
	}
	base := newBaseScheduler(opController)
	return &evictLeaderScheduler{
		baseScheduler: base,
		name:          name,
		storeID:       storeID,
		selector:      selector.NewRandomSelector(filters),
	}
}

func (s *evictLeaderScheduler) GetName() string {
	return s.name
}

func (s *evictLeaderScheduler) GetType() string {
	return "evict-leader"
}

func (s *evictLeaderScheduler) Prepare(cluster opt.Cluster) error {
	return cluster.BlockStore(s.storeID)
}

func (s *evictLeaderScheduler) Cleanup(cluster opt.Cluster) {
	cluster.UnblockStore(s.storeID)
}

func (s *evictLeaderScheduler) IsScheduleAllowed(cluster opt.Cluster) bool {
	return s.opController.OperatorCount(operator.OpLeader) < cluster.GetLeaderScheduleLimit()
}

func (s *evictLeaderScheduler) Schedule(cluster opt.Cluster) []*operator.Operator {
	schedulerCounter.WithLabelValues(s.GetName(), "schedule").Inc()
	region := cluster.RandLeaderRegion(s.storeID, core.HealthRegion())
	if region == nil {
		schedulerCounter.WithLabelValues(s.GetName(), "no-leader").Inc()
		return nil
	}
	target := s.selector.SelectTarget(cluster, cluster.GetFollowerStores(region))
	if target == nil {
		schedulerCounter.WithLabelValues(s.GetName(), "no-target-store").Inc()
		return nil
	}
	schedulerCounter.WithLabelValues(s.GetName(), "new-operator").Inc()
	op := operator.CreateTransferLeaderOperator("evict-leader", region, region.GetLeader().GetStoreId(), target.GetID(), operator.OpLeader)
	op.SetPriorityLevel(core.HighPriority)
	return []*operator.Operator{op}
}
