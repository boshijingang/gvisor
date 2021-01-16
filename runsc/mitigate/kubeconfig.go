// Copyright 2021 The gVisor Authors.
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

package mitigate

import (
	"fmt"
	"strings"

	yaml "gopkg.in/yaml.v2"
)

type kubeconfig struct {
	config map[interface{}]interface{}
}

const (
	kubeconfigSuffix     = "KUBE_SCHEDULER_CONFIG\n" // suffix for GKE kubelet-config.yaml files.
	kubeReservedField    = "kubeReserved"
	cpuField             = "cpu"
	gkeCustomReservedCPU = "1060m" // GKE sets 1060m for several CPU classes (e.g. e2-medium) ignoring calculating kubeReserved.cpu values. See below.
)

// getKubeconfig returns a kubeconfig from a read kubelet-config.yaml.
func getKubeconfig(data []byte) (*kubeconfig, error) {
	data = []byte(strings.TrimSuffix(string(data), kubeconfigSuffix))
	ret := &kubeconfig{}
	ret.config = make(map[interface{}]interface{})
	err := yaml.Unmarshal(data, &ret.config)
	return ret, err
}

// unpack returns a []byte for writing to a kubelet-config.yaml file.
func (k *kubeconfig) unpack() ([]byte, error) {
	ret, err := yaml.Marshal(k.config)
	return append(ret, []byte(kubeconfigSuffix)...), err
}

// computeReservedCPU returns a value for the kubeReserved.cpu field.
// See: https://cloud.google.com/kubernetes-engine/docs/concepts/cluster-architecture#memory_cpu
func (k *kubeconfig) computeReservedCPU(cpus int64) (string, error) {
	// For several Machine Types (e2-medium, e2-small, etc) GKE
	// sets the kubeReserved.cpu field to 1060m (.94 Allocatable CPU).
	// If the field is that value, return it as is.
	if cpus <= 2 {
		val, err := k.getReservedCPU()
		if err != nil || val == gkeCustomReservedCPU {
			return val, err
		}
	}

	totals := make([]float64, cpus)

	// GKE's computation of the reserved CPU field.
	for _, p := range []struct {
		percentage float64 // Percentage of CPU for this range.
		minCPU     int64   // Minimum CPU for this percentage.
		maxCPU     int64   // Maximum CPU for this percentage.
	}{
		{
			// Take 6% from the first CPU.
			percentage: 0.06,
			minCPU:     0,
			maxCPU:     1,
		}, {
			// Take 1% from the second CPU.
			percentage: 0.01,
			minCPU:     1,
			maxCPU:     2,
		}, {
			// Take 0.5 % from the next two CPUs.
			percentage: 0.005,
			minCPU:     2,
			maxCPU:     4,
		}, {
			// Take 0.25% from the remaining CPUs.
			percentage: 0.0025,
			minCPU:     4,
			maxCPU:     cpus,
		},
	} {
		for i := p.minCPU; i < cpus && i < p.maxCPU; i++ {
			// Compute totals in milliCPUs.
			totals[i] = 1000 * p.percentage
		}
	}

	// Aggregate the totals and return the result formatted
	// for the YAML file (e.g. 360m).
	milliCPUs := 0.0
	for _, total := range totals {
		milliCPUs += total
	}

	return fmt.Sprintf("%dm", int64(milliCPUs)), nil
}

// setReservedCPU sets the kubeResereved.cpu field.
func (k *kubeconfig) setReservedCPU(reserved string) error {
	return k.setField([]string{kubeReservedField, cpuField}, reserved)
}

// getReservedCPU gets the kubeReserved.cpu field.
func (k *kubeconfig) getReservedCPU() (string, error) {
	return k.getFieldAsString([]string{kubeReservedField, cpuField})
}

// setField sets a generic field. The field is assumed to be
// under a tree of maps, which are searched in order indexed by
// each field in fields (e.g. k.config[field[0]][field[1]]...).
func (k *kubeconfig) setField(fields []string, value string) error {
	result, err := k.getSubfield(fields[:len(fields)-1], false /*get*/)
	if err != nil {
		return fmt.Errorf("failed to set field: %v", err)
	}

	field := fields[len(fields)-1]
	r, ok := result.(map[interface{}]interface{})
	if !ok {
		return fmt.Errorf("field %s not in result: %+v", field, result)
	}
	r[field] = value
	return nil
}

// getFieldAsString returns a given field as a string. Fields are searched
// as in setField().
func (k *kubeconfig) getFieldAsString(fields []string) (string, error) {
	val, err := k.getSubfield(fields, true /*get*/)
	if err != nil {
		return "", err
	}
	ret, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("reservedCPU field not string: %v", val)
	}
	return ret, nil
}

// getSubfield a generic subfield and returns it as an interface.
// Fields ar searched as in setField().
func (k *kubeconfig) getSubfield(fields []string, get bool) (interface{}, error) {
	result := interface{}(k.config)
	for _, field := range fields {
		r, ok := result.(map[interface{}]interface{})
		if !ok {
			return nil, fmt.Errorf("result %v is not a map on field %s", result, field)
		}
		result, ok = r[field]
		// in the get case, we do not want to make new fields.
		if !ok && get {
			return nil, fmt.Errorf("field %s does not exist: %+v", field, r)
		}
		// otherwise this is a set operation and we make fields as we go.
		if !ok {
			r[field] = make(map[interface{}]interface{})
			result = r[field]
		}
	}
	return result, nil
}
