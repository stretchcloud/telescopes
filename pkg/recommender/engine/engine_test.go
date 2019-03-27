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
	"testing"

	"github.com/banzaicloud/cloudinfo/pkg/cloudinfo-client/models"
	"github.com/banzaicloud/telescopes/pkg/recommender"
	"github.com/goph/logur"
	"github.com/stretchr/testify/assert"
)

type dummyProducts struct {
	// test case id to drive the behaviour
	TcId string
}

func (p *dummyProducts) GetProductDetails(provider string, service string, region string) ([]*models.ProductDetails, error) {
	return []*models.ProductDetails{
		{
			Type:          "type-3",
			CurrentGen:    true,
			OnDemandPrice: 0.023,
			Cpus:          1,
			Mem:           2,
			SpotPrice:     []*models.ZonePrice{{Price: 0.0069, Zone: "dummyZone1"}},
		},
		{
			Type:          "type-4",
			CurrentGen:    true,
			OnDemandPrice: 0.096,
			Cpus:          2,
			Mem:           4,
			SpotPrice:     []*models.ZonePrice{{Price: 0.018, Zone: "dummyZone2"}},
		},
		{
			Type:          "type-5",
			CurrentGen:    true,
			OnDemandPrice: 0.046,
			Cpus:          2,
			Mem:           4,
			SpotPrice:     []*models.ZonePrice{{Price: 0.014, Zone: "dummyZone2"}},
		},
		{
			Type:          "type-6",
			CurrentGen:    true,
			OnDemandPrice: 0.096,
			Cpus:          2,
			Mem:           8,
			SpotPrice:     []*models.ZonePrice{{Price: 0.02, Zone: "dummyZone1"}},
		},
		{
			Type:          "type-7",
			CurrentGen:    true,
			OnDemandPrice: 0.17,
			Cpus:          4,
			Mem:           8,
			SpotPrice:     []*models.ZonePrice{{Price: 0.037, Zone: "dummyZone3"}},
		},
		{
			Type:          "type-8",
			CurrentGen:    true,
			OnDemandPrice: 0.186,
			Cpus:          4,
			Mem:           16,
			SpotPrice:     []*models.ZonePrice{{Price: 0.056, Zone: "dummyZone2"}},
		},
		{
			Type:          "type-9",
			CurrentGen:    true,
			OnDemandPrice: 0.34,
			Cpus:          8,
			Mem:           16,
			SpotPrice:     []*models.ZonePrice{{Price: 0.097, Zone: "dummyZone1"}},
		},
		{
			Type:          "type-10",
			CurrentGen:    true,
			OnDemandPrice: 0.68,
			Cpus:          17,
			Mem:           32,
			SpotPrice:     []*models.ZonePrice{{Price: 0.171, Zone: "dummyZone2"}},
		},
		{
			Type:          "type-11",
			CurrentGen:    true,
			OnDemandPrice: 0.91,
			Cpus:          16,
			Mem:           64,
			SpotPrice:     []*models.ZonePrice{{Price: 0.157, Zone: "dummyZone1"}},
		},
		{
			Type:          "type-12",
			CurrentGen:    true,
			OnDemandPrice: 1.872,
			Cpus:          32,
			Mem:           128,
			SpotPrice:     []*models.ZonePrice{{Price: 0.66, Zone: "dummyZone3"}},
		},
	}, nil
}

func (p *dummyProducts) GetRegions(provider, service string) ([]*models.Continent, error) {
	return nil, nil
}

func TestEngine_RecommendCluster(t *testing.T) {
	tests := []struct {
		name     string
		ciSource recommender.CloudInfoSource
		request  recommender.ClusterRecommendationReq
		check    func(resp *recommender.ClusterRecommendationResp, err error)
	}{
		{
			name:     "cluster recommendation success",
			ciSource: &dummyProducts{},
			request: recommender.ClusterRecommendationReq{
				MinNodes: 1,
				MaxNodes: 1,
				SumMem:   32,
				SumCpu:   16,
			},
			check: func(resp *recommender.ClusterRecommendationResp, err error) {
				assert.Nil(t, err, "the error should be nil")
				assert.Equal(t, float64(64), resp.Accuracy.RecMem)
				assert.Equal(t, float64(16), resp.Accuracy.RecCpu)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := NewEngine(test.ciSource, logur.NewTestLogger())

			test.check(engine.RecommendCluster("dummyProvider", "dummyService", "dummyRegion", test.request, nil))
		})
	}
}

func TestEngine_findCheapestNodePoolSet(t *testing.T) {
	tests := []struct {
		name      string
		nodePools map[string][]recommender.NodePool
		check     func(npo []recommender.NodePool)
	}{
		{
			name: "find cheapest node pool set",
			nodePools: map[string][]recommender.NodePool{
				recommender.Memory: {
					recommender.NodePool{ // price = 2*3 +2*2 = 10
						VmType: recommender.VirtualMachine{
							AvgPrice:      2,
							OnDemandPrice: 3,
						},
						SumNodes: 2,
						VmClass:  recommender.Regular,
					}, recommender.NodePool{
						VmType: recommender.VirtualMachine{
							AvgPrice:      2,
							OnDemandPrice: 3,
						},
						SumNodes: 2,
						VmClass:  recommender.Spot,
					},
				},
				recommender.Cpu: { // price = 2*2 +2*2 = 8
					recommender.NodePool{
						VmType: recommender.VirtualMachine{
							AvgPrice:      1,
							OnDemandPrice: 2,
						},
						SumNodes: 2,
						VmClass:  recommender.Regular,
					}, recommender.NodePool{
						VmType: recommender.VirtualMachine{
							AvgPrice:      2,
							OnDemandPrice: 4,
						},
						SumNodes: 2,
						VmClass:  recommender.Spot,
					}, recommender.NodePool{
						VmType: recommender.VirtualMachine{
							AvgPrice:      2,
							OnDemandPrice: 4,
						},
						SumNodes: 0,
						VmClass:  recommender.Spot,
					},
				},
			},
			check: func(npo []recommender.NodePool) {
				assert.Equal(t, 3, len(npo), "wrong selection")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := NewEngine(nil, logur.NewTestLogger())
			test.check(engine.findCheapestNodePoolSet(test.nodePools))
		})
	}
}
