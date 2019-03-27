// Copyright © 2019 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"fmt"
	"math"
	"sort"

	"github.com/banzaicloud/telescopes/internal/platform/log"
	"github.com/banzaicloud/telescopes/pkg/recommender"
	"github.com/banzaicloud/telescopes/pkg/recommender/nodepools"
	"github.com/banzaicloud/telescopes/pkg/recommender/vms"
	"github.com/goph/emperror"
	"github.com/goph/logur"
	"github.com/pkg/errors"
)

// Engine represents the recommendation engine, it operates on a map of provider -> VmRegistry
type Engine struct {
	ciSource recommender.CloudInfoSource
	log      logur.Logger
}

// ClusterRecommender is the main entry point for cluster recommendation
type ClusterRecommender interface {

	// RecommendCluster performs recommendation based on the provided arguments
	RecommendCluster(provider string, service string, region string, req recommender.ClusterRecommendationReq, layoutDesc []recommender.NodePoolDesc) (*recommender.ClusterRecommendationResp, error)

	// RecommendClusterScaleOut performs recommendation for an existing layout's scale out
	RecommendClusterScaleOut(provider string, service string, region string, req recommender.ClusterScaleoutRecommendationReq) (*recommender.ClusterRecommendationResp, error)

	// RecommendClusters performs recommendation
	RecommendClusters(req recommender.Request, logger logur.Logger) (map[string][]*recommender.ClusterRecommendationResp, error)
}

// NewEngine creates a new Engine instance
func NewEngine(ciSource recommender.CloudInfoSource, log logur.Logger) *Engine {
	return &Engine{
		ciSource: ciSource,
		log:      log,
	}
}

// RecommendCluster performs recommendation based on the provided arguments
func (e *Engine) RecommendCluster(provider string, service string, region string, req recommender.ClusterRecommendationReq, layoutDesc []recommender.NodePoolDesc) (*recommender.ClusterRecommendationResp, error) {
	e.log.Info(fmt.Sprintf("recommending cluster configuration. request: [%#v]", req))

	desiredCpu := req.SumCpu
	desiredMem := req.SumMem
	desiredOdPct := req.OnDemandPct

	attributes := []string{recommender.Cpu, recommender.Memory}
	nodePools := make(map[string][]recommender.NodePool, 2)

	vmSelector := vms.NewVmSelector(e.log)

	npSelector := nodepools.NewNodePoolSelector(e.log)

	allProducts, err := e.ciSource.GetProductDetails(provider, service, region)
	if err != nil {
		return nil, err
	}

	//todo add request validation for interdependent request fields, eg: onDemandPct is always 100 when spot
	// instances are not available for provider
	if provider == "oracle" || provider == "alibaba" {
		e.log.Warn("onDemand percentage in the request ignored")
		req.OnDemandPct = 100
	}

	for _, attr := range attributes {
		vmsInRange, err := vmSelector.FindVmsWithAttrValues(provider, service, region, attr, req, layoutDesc, allProducts)
		if err != nil {
			return nil, emperror.With(err, recommender.RecommenderErrorTag, "vms")
		}

		layout := e.transformLayout(layoutDesc, vmsInRange)
		if layout != nil {
			req.SumCpu, req.SumMem, req.OnDemandPct, err = e.computeScaleoutResources(layout, attr, desiredCpu, desiredMem, desiredOdPct)
			if err != nil {
				e.log.Error(emperror.Wrap(err, "failed to compute scaleout resources").Error())
				continue
			}
			if req.SumCpu < 0 && req.SumMem < 0 {
				return nil, emperror.With(fmt.Errorf("there's already enough resources in the cluster. Total resources available: CPU: %v, Mem: %v", desiredCpu-req.SumCpu, desiredMem-req.SumMem))
			}
		}

		odVms, spotVms, err := vmSelector.RecommendVms(provider, vmsInRange, attr, req, layout)
		if err != nil {
			return nil, emperror.Wrap(err, "failed to recommend virtual machines")
		}

		if (len(odVms) == 0 && req.OnDemandPct > 0) || (len(spotVms) == 0 && req.OnDemandPct < 100) {
			e.log.Debug("no vms with the requested resources found", map[string]interface{}{"attribute": attr})
			// skip the nodepool creation, go to the next attr
			continue
		}
		e.log.Debug("recommended on-demand vms", map[string]interface{}{"attribute": attr, "count": len(odVms), "values": odVms})
		e.log.Debug("recommended spot vms", map[string]interface{}{"attribute": attr, "count": len(odVms), "values": odVms})

		nps := npSelector.RecommendNodePools(attr, req, layout, odVms, spotVms)

		e.log.Debug(fmt.Sprintf("recommended node pools for [%s]: count:[%d] , values: [%#v]", attr, len(nps), nps))

		nodePools[attr] = nps

	}

	if len(nodePools) == 0 {
		e.log.Debug(fmt.Sprintf("could not recommend node pools for request: %v", req))
		return nil, emperror.With(errors.New("could not recommend cluster with the requested resources"), recommender.RecommenderErrorTag)
	}

	cheapestNodePoolSet := e.findCheapestNodePoolSet(nodePools)

	accuracy := findResponseSum(req.Zones, cheapestNodePoolSet)

	return &recommender.ClusterRecommendationResp{
		Provider:  provider,
		Service:   service,
		Region:    region,
		Zones:     req.Zones,
		NodePools: cheapestNodePoolSet,
		Accuracy:  accuracy,
	}, nil
}

// RecommendClusterScaleOut performs recommendation for an existing layout's scale out
func (e *Engine) RecommendClusterScaleOut(provider string, service string, region string, req recommender.ClusterScaleoutRecommendationReq) (*recommender.ClusterRecommendationResp, error) {
	e.log.Info(fmt.Sprintf("recommending cluster configuration. request: [%#v]", req))

	includes := make([]string, len(req.ActualLayout))
	for i, npd := range req.ActualLayout {
		includes[i] = npd.InstanceType
	}

	clReq := recommender.ClusterRecommendationReq{
		Zones:         req.Zones,
		AllowBurst:    boolPointer(true),
		Includes:      includes,
		Excludes:      req.Excludes,
		AllowOlderGen: boolPointer(true),
		MaxNodes:      math.MaxInt8,
		MinNodes:      1,
		NetworkPerf:   nil,
		OnDemandPct:   req.OnDemandPct,
		SameSize:      false,
		SumCpu:        req.DesiredCpu,
		SumMem:        req.DesiredMem,
		SumGpu:        req.DesiredGpu,
	}

	return e.RecommendCluster(provider, service, region, clReq, req.ActualLayout)
}

// RecommendClusters performs recommendation
func (e *Engine) RecommendClusters(req recommender.Request) (map[string][]*recommender.ClusterRecommendationResp, error) {
	respPerService := make(map[string][]*recommender.ClusterRecommendationResp)

	for _, provider := range req.Providers {
		for _, service := range provider.Services {
			continents, err := e.ciSource.GetRegions(provider.Provider, service)
			if err != nil {
				return nil, err
			}
			if req.Continents != nil {
				for _, validContinent := range req.Continents {
					for _, continent := range continents {
						if validContinent == continent.Name {
							for _, region := range continent.Regions {
								e.log = log.WithFields(e.log, map[string]interface{}{"provider": provider.Provider, "service": service, "region": region.ID})
								if response, err := e.RecommendCluster(provider.Provider, service, region.ID, req.Request, nil); err != nil {
									e.log.Warn("could not recommend cluster")
								} else {
									respPerService[response.Service] = append(respPerService[response.Service], response)
								}
							}
						}
					}
				}
			} else {
				for _, continent := range continents {
					for _, region := range continent.Regions {
						e.log = log.WithFields(e.log, map[string]interface{}{"provider": provider.Provider, "service": service, "region": region.ID})
						if response, err := e.RecommendCluster(provider.Provider, service, region.ID, req.Request, nil); err != nil {
							e.log.Warn("could not recommend cluster")
						} else {
							respPerService[response.Service] = append(respPerService[response.Service], response)
						}
					}
				}
			}
			sort.Sort(ByPricePerService(respPerService[service]))

			if len(respPerService[service]) > req.Request.RespPerService {
				var limit = 0
				for i := range respPerService[service] {
					if respPerService[service][req.Request.RespPerService-1].Accuracy.RecTotalPrice < respPerService[service][i].Accuracy.RecTotalPrice {
						limit = i
						break
					}
				}
				if limit != 0 {
					respPerService[service] = respPerService[service][:limit]
				}
			}
		}
	}

	if len(respPerService) == 0 {
		return nil, errors.New("failed to recommend clusters")
	}

	return respPerService, nil
}

// ByPricePerService type for custom sorting of a slice of response
type ByPricePerService []*recommender.ClusterRecommendationResp

func (a ByPricePerService) Len() int      { return len(a) }
func (a ByPricePerService) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByPricePerService) Less(i, j int) bool {
	totalPrice1 := a[i].Accuracy.RecTotalPrice
	totalPrice2 := a[j].Accuracy.RecTotalPrice
	return totalPrice1 < totalPrice2
}

func boolPointer(b bool) *bool {
	return &b
}

func findResponseSum(zones []string, nodePoolSet []recommender.NodePool) recommender.ClusterRecommendationAccuracy {
	var sumCpus float64
	var sumMem float64
	var sumNodes int
	var sumRegularPrice float64
	var sumRegularNodes int
	var sumSpotPrice float64
	var sumSpotNodes int
	var sumTotalPrice float64
	for _, nodePool := range nodePoolSet {
		sumCpus += nodePool.GetSum(recommender.Cpu)
		sumMem += nodePool.GetSum(recommender.Memory)
		sumNodes += nodePool.SumNodes
		if nodePool.VmClass == recommender.Regular {
			sumRegularPrice += nodePool.PoolPrice()
			sumRegularNodes += nodePool.SumNodes
		} else {
			sumSpotPrice += nodePool.PoolPrice()
			sumSpotNodes += nodePool.SumNodes
		}
		sumTotalPrice += nodePool.PoolPrice()
	}

	return recommender.ClusterRecommendationAccuracy{
		RecCpu:          sumCpus,
		RecMem:          sumMem,
		RecNodes:        sumNodes,
		RecZone:         zones,
		RecRegularPrice: sumRegularPrice,
		RecRegularNodes: sumRegularNodes,
		RecSpotPrice:    sumSpotPrice,
		RecSpotNodes:    sumSpotNodes,
		RecTotalPrice:   sumTotalPrice,
	}
}

func (e *Engine) transformLayout(layoutDesc []recommender.NodePoolDesc, vms []recommender.VirtualMachine) []recommender.NodePool {
	if layoutDesc == nil {
		return nil
	}
	nps := make([]recommender.NodePool, len(layoutDesc))
	for i, npd := range layoutDesc {
		for _, vm := range vms {
			if vm.Type == npd.InstanceType {
				nps[i] = recommender.NodePool{
					VmType:   vm,
					VmClass:  npd.GetVmClass(),
					SumNodes: npd.SumNodes,
				}
				break
			}
		}
	}
	return nps
}

// findCheapestNodePoolSet looks up the "cheapest" node pool set from the provided map
func (e *Engine) findCheapestNodePoolSet(nodePoolSets map[string][]recommender.NodePool) []recommender.NodePool {
	e.log.Info("finding cheapest pool set...")
	var cheapestNpSet []recommender.NodePool
	var bestPrice float64

	for attr, nodePools := range nodePoolSets {
		var sumPrice float64
		var sumCpus float64
		var sumMem float64

		for _, np := range nodePools {
			sumPrice += np.PoolPrice()
			sumCpus += np.GetSum(recommender.Cpu)
			sumMem += np.GetSum(recommender.Memory)
		}
		e.log.Debug("checking node pool",
			map[string]interface{}{"attribute": attr, "cpu": sumCpus, "memory": sumMem, "price": sumPrice})

		if bestPrice == 0 || bestPrice > sumPrice {
			e.log.Debug("cheaper node pool set is found", map[string]interface{}{"price": sumPrice})
			bestPrice = sumPrice
			cheapestNpSet = nodePools
		}
	}
	return cheapestNpSet
}

func (e *Engine) computeScaleoutResources(layout []recommender.NodePool, attr string, desiredCpu, desiredMem float64, desiredOdPct int) (float64, float64, int, error) {
	var currentCpuTotal, currentMemTotal, sumCurrentOdCpu, sumCurrentOdMem float64
	var scaleoutOdPct int
	for _, np := range layout {
		if np.VmClass == recommender.Regular {
			sumCurrentOdCpu += float64(np.SumNodes) * np.VmType.Cpus
			sumCurrentOdMem += float64(np.SumNodes) * np.VmType.Mem
		}
		currentCpuTotal += float64(np.SumNodes) * np.VmType.Cpus
		currentMemTotal += float64(np.SumNodes) * np.VmType.Mem
	}

	scaleoutCpu := desiredCpu - currentCpuTotal
	scaleoutMem := desiredMem - currentMemTotal

	if scaleoutCpu < 0 && scaleoutMem < 0 {
		return scaleoutCpu, scaleoutMem, 0, nil
	}

	e.log.Debug(fmt.Sprintf("desiredCpu: %v, desiredMem: %v, currentCpuTotal/currentCpuOnDemand: %v/%v, currentMemTotal/currentMemOnDemand: %v/%v", desiredCpu, desiredMem, currentCpuTotal, sumCurrentOdCpu, currentMemTotal, sumCurrentOdMem))
	e.log.Debug(fmt.Sprintf("total scaleout cpu/mem needed: %v/%v", scaleoutCpu, scaleoutMem))
	e.log.Debug(fmt.Sprintf("desired on-demand percentage: %v", desiredOdPct))

	switch attr {
	case recommender.Cpu:
		if scaleoutCpu < 0 {
			return 0, 0, 0, errors.New("there's already enough CPU resources in the cluster")
		}
		desiredOdCpu := desiredCpu * float64(desiredOdPct) / 100
		scaleoutOdCpu := desiredOdCpu - sumCurrentOdCpu
		scaleoutOdPct = int(scaleoutOdCpu / scaleoutCpu * 100)
		e.log.Debug(fmt.Sprintf("desired on-demand cpu: %v, cpu to add with the scaleout: %v", desiredOdCpu, scaleoutOdCpu))
	case recommender.Memory:
		if scaleoutMem < 0 {
			return 0, 0, 0, emperror.With(errors.New("there's already enough memory resources in the cluster"))
		}
		desiredOdMem := desiredMem * float64(desiredOdPct) / 100
		scaleoutOdMem := desiredOdMem - sumCurrentOdMem
		e.log.Debug(fmt.Sprintf("desired on-demand memory: %v, memory to add with the scaleout: %v", desiredOdMem, scaleoutOdMem))
		scaleoutOdPct = int(scaleoutOdMem / scaleoutMem * 100)
	}
	if scaleoutOdPct > 100 {
		// even if we add only on-demand instances, we still we can't reach the minimum ratio
		return 0, 0, 0, emperror.With(errors.New("couldn't scale out cluster with the provided parameters"), "onDemandPct", desiredOdPct)
	} else if scaleoutOdPct < 0 {
		// means that we already have enough resources in the cluster to keep the minimum ratio
		scaleoutOdPct = 0
	}
	e.log.Debug(fmt.Sprintf("percentage of on-demand resources in the scaleout: %v", scaleoutOdPct))
	return scaleoutCpu, scaleoutMem, scaleoutOdPct, nil
}
