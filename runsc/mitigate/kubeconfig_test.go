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
	"testing"

	"github.com/google/go-cmp/cmp"
)

const sampleYAML = `apiVersion: kubelet.config.k8s.io/v1beta1
authentication:
  anonymous:
    enabled: false
  webhook:
    enabled: true
  x509:
    clientCAFile: /etc/srv/kubernetes/pki/ca-certificates.crt
authorization:
  mode: Webhook
cgroupRoot: /
clusterDNS:
- 10.3.240.10
clusterDomain: cluster.local
enableDebuggingHandlers: true
evictionHard:
  memory.available: 100Mi
  nodefs.available: 10%
  nodefs.inodesFree: 5%
  pid.available: 10%
featureGates:
  DynamicKubeletConfig: false
  RotateKubeletServerCertificate: true
kernelMemcgNotification: true
kind: KubeletConfiguration
kubeReserved:
  cpu: 1060m
  ephemeral-storage: 41Gi
  memory: 1019Mi
readOnlyPort: 10255
serverTLSBootstrap: true
staticPodPath: /etc/kubernetes/manifests
KUBE_SCHEDULER_CONFIG
`

// TestYamlParser tests basic Yaml parsing.
func TestYamlParser(t *testing.T) {
	before, err := getKubeconfig([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("Failed to parse yaml: %v", err)
	}

	got, err := before.unpack()
	if err != nil {
		t.Fatalf("Failed to unpack kubeconfig: %v", err)
	}

	if diff := cmp.Diff(string(got), sampleYAML); diff != "" {
		t.Fatalf("got different yaml files (-got, want)\n%s", diff)
	}
}

// TestGetSetFields tests the getting and setting fields.
func TestGetSetFields(t *testing.T) {
	wantTemplate := `kubeReserved:
  cpu: %s
  ephemeral-storage: 41Gi
  memory: 1019Mi
` + kubeconfigSuffix

	for _, tc := range []struct {
		name           string
		yaml           string
		shouldError    bool
		reservedCPUVal string
	}{
		{
			name: "exampleYAML",
			yaml: `kubeReserved:
  cpu: 1060m
  ephemeral-storage: 41Gi
  memory: 1019Mi` + kubeconfigSuffix,
			reservedCPUVal: "50m",
		},
		{
			name:        "notAMap",
			yaml:        `kubeReserved: NotAMap`,
			shouldError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := getKubeconfig([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("Failed to parse yaml: %v", err)
			}

			err = config.setReservedCPU(tc.reservedCPUVal)
			if tc.shouldError {
				if err != nil {
					t.Logf("Test got error: %v", err)
					return
				}
				t.Fatalf("Test expected error: got: nil")
			}

			if err != nil {
				t.Fatalf("Set reservedCPU failed: %v", err)
			}

			got, err := config.getReservedCPU()
			if err != nil {
				t.Fatalf("Failed to get kubeReserved.cpu: %v", err)
			}
			if got != tc.reservedCPUVal {
				t.Fatalf("reservedCPU: got: %s want: %s", got, tc.reservedCPUVal)
			}

			got2, err := config.unpack()
			if err != nil {
				t.Fatalf("Failed to unpack result: %v", err)
			}

			want := fmt.Sprintf(wantTemplate, tc.reservedCPUVal)
			if result := cmp.Diff(string(got2), want); result != "" {
				t.Fatalf("Comparison failed (-got, +want)\n%s", result)
			}
		})
	}
}

func TestComputeCPUs(t *testing.T) {
	// basicYAML represets the basic case
	// GKE has not set kubeReserved.cpu to a special value.
	// See: https://cloud.google.com/kubernetes-engine/docs/concepts/cluster-architecture#memory_cpu
	yamlTemplate := `kubeReserved:
  cpu: %s
  ephemeral-storage: 41Gi
  memory: 1019Mi` + kubeconfigSuffix

	basicYAML := fmt.Sprintf(yamlTemplate, "0m")

	// specialYAML represents cases where GKE
	// has set kubeReserved.cpu to a special value (e.g. e2-medium).
	specialYAML := fmt.Sprintf(yamlTemplate, gkeCustomReservedCPU)

	for _, tc := range []struct {
		name            string
		CPUs            int64
		allocatableCPUS int64
		yaml            string
	}{
		{
			name:            "c2-standard-16",
			CPUs:            16,
			allocatableCPUS: 15890,
			yaml:            basicYAML,
		},
		{
			name:            "n2-standard-8",
			CPUs:            8,
			allocatableCPUS: 7910,
			yaml:            basicYAML,
		},
		{
			name:            "n1-standard-32",
			CPUs:            32,
			allocatableCPUS: 31850,
			yaml:            basicYAML,
		},
		{
			name:            "e2-medium",
			CPUs:            2,
			allocatableCPUS: 940,
			yaml:            specialYAML,
		},
		{
			name:            "n2d-highcpu-64",
			CPUs:            64,
			allocatableCPUS: 63770,
			yaml:            basicYAML,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := getKubeconfig([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("Failed to parse YAML: %v", err)
			}

			want := fmt.Sprintf("%dm", (tc.CPUs*1000)-tc.allocatableCPUS)

			got, err := config.computeReservedCPU(tc.CPUs)
			if err != nil {
				t.Fatalf("Failed to compute reservedCPU: %v", err)
			}

			if got != want {
				t.Fatalf("reservedCPU mismatch: got: %s want: %s", got, want)
			}
		})
	}
}
