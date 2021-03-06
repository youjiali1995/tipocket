// Copyright 2019 PingCAP, Inc.
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

package cdc

import (
	"context"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql" // mysql driver
	"github.com/ngaut/log"
	"github.com/pingcap/errors"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	clusterTypes "github.com/pingcap/tipocket/pkg/cluster/types"
	"github.com/pingcap/tipocket/pkg/test-infra/tidb"
	"github.com/pingcap/tipocket/pkg/test-infra/util"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Ops knows how to operate TiDB CDC on k8s
type Ops struct {
	cli     client.Client
	TidbOps *tidb.Ops
}

// New creates cdc ops
func New(cli client.Client, tidbClient *tidb.Ops) *Ops {
	return &Ops{cli, tidbClient}
}

// Apply CDC cluster
func (o *Ops) Apply(tc *Recommendation) error {
	var g errgroup.Group
	g.Go(func() error {
		return o.TidbOps.ApplyTiDBCluster(tc.Upstream)
	})
	g.Go(func() error {
		return o.TidbOps.ApplyTiDBCluster(tc.Downstream)
	})
	if err := g.Wait(); err != nil {
		return err
	}

	if err := o.ApplyCDC(tc.CDC); err != nil {
		return err
	}

	if err := o.applyJob(tc.CDC.Job); err != nil {
		return err
	}
	return nil
}

// Delete CDC cluster
func (o *Ops) Delete(tc *Recommendation) error {
	if err := o.TidbOps.Delete(tc.Upstream); err != nil {
		return err
	}
	if err := o.DeleteCDC(tc.CDC); err != nil {
		return err
	}
	if err := o.TidbOps.Delete(tc.Downstream); err != nil {
		return err
	}
	return nil
}

// ApplyCDC ...
func (o *Ops) ApplyCDC(cc *CDC) error {
	if err := o.applyObject(cc.Service); err != nil {
		return err
	}
	if err := o.applyObject(cc.StatefulSet); err != nil {
		return err
	}

	if err := o.waitCDCReady(cc.StatefulSet, 5*time.Minute); err != nil {
		return err
	}
	return nil
}

func (o *Ops) waitCDCReady(st *appsv1.StatefulSet, timeout time.Duration) error {
	local := st.DeepCopy()
	log.Infof("Waiting up to %v for StatefulSet %s to have all replicas ready",
		timeout, st.Name)
	return wait.PollImmediate(5*time.Second, timeout, func() (bool, error) {
		key, err := client.ObjectKeyFromObject(local)
		if err != nil {
			return false, err
		}

		if err = o.cli.Get(context.TODO(), key, local); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			log.Errorf("error getting CDC statefulset: %v", err)
			return false, err
		}

		if local.Status.ReadyReplicas != *local.Spec.Replicas {
			log.Infof("CDC %s do not have enough ready replicas, ready: %d, desired: %d",
				local.Name, local.Status.ReadyReplicas, *local.Spec.Replicas)
			return false, nil
		}
		log.Infof("All %d replicas of CDC %s are ready.", local.Status.ReadyReplicas, local.Name)
		return true, nil
	})
}

func (o *Ops) waitJobCompleted(job *batchv1.Job) error {
	local := job.DeepCopy()
	log.Infof("Waiting up to %s for job completed", local.Name)

	return wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		key, err := client.ObjectKeyFromObject(local)
		if err != nil {
			return false, err
		}

		if err = o.cli.Get(context.TODO(), key, local); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			log.Errorf("error getting CDC job: %v", err)
			return false, err
		}

		if local.Status.Succeeded != 1 {
			log.Infof("Job %s was not completed.", local.Name)
			return false, nil
		}

		log.Infof("Job %s has been completed.", local.Name)
		return true, nil
	})
}

func (o *Ops) applyObject(object runtime.Object) error {
	if err := o.cli.Create(context.TODO(), object); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

// DeleteCDC ...
func (o *Ops) DeleteCDC(cc *CDC) error {
	if err := o.cli.Delete(context.TODO(), cc.Service); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := o.cli.Delete(context.TODO(), cc.StatefulSet); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (o *Ops) applyJob(job *batchv1.Job) error {
	if err := o.applyObject(job); err != nil {
		return err
	}
	return o.waitJobCompleted(job)
}

// GetClientNodes returns the client nodes
func (o *Ops) GetClientNodes(tc *Recommendation) ([]clusterTypes.ClientNode, error) {
	var clientNodes []clusterTypes.ClientNode
	upstreamClientNodes, err := o.TidbOps.GetClientNodes(tc.Upstream)
	if err != nil {
		return clientNodes, err
	}
	downstreamClientNodes, err := o.TidbOps.GetClientNodes(tc.Downstream)
	if err != nil {
		return clientNodes, err
	}
	clientNodes = append(upstreamClientNodes, downstreamClientNodes...)

	if len(clientNodes) != 2 {
		return clientNodes, errors.New("clientNodes count not 2")
	}

	return clientNodes, nil
}

// GetNodes returns all nodes
func (o *Ops) GetNodes(tc *Recommendation) ([]clusterTypes.Node, error) {
	var nodes []clusterTypes.Node

	upstreamNodes, err := o.TidbOps.GetNodes(tc.Upstream)
	if err != nil {
		return nodes, err
	}
	downstreamNodes, err := o.TidbOps.GetNodes(tc.Downstream)
	if err != nil {
		return nodes, err
	}
	cdcNode, err := o.GetCDCNode(tc.CDC)
	if err != nil {
		return nodes, err
	}

	return append(append(upstreamNodes, downstreamNodes...), cdcNode), nil
}

// GetCDCNode returns the nodes of cdc
func (o *Ops) GetCDCNode(cdc *CDC) (clusterTypes.Node, error) {
	pod := &corev1.Pod{}
	err := o.cli.Get(context.Background(), client.ObjectKey{
		Namespace: cdc.StatefulSet.ObjectMeta.Namespace,
		Name:      fmt.Sprintf("%s-0", cdc.StatefulSet.ObjectMeta.Name),
	}, pod)

	if err != nil {
		return clusterTypes.Node{}, err
	}

	return clusterTypes.Node{
		Namespace: pod.ObjectMeta.Namespace,
		PodName:   pod.ObjectMeta.Name,
		IP:        pod.Status.PodIP,
		Component: clusterTypes.CDC,
		Port:      util.FindPort(pod.ObjectMeta.Name, pod.Spec.Containers[0].Ports),
	}, nil
}
