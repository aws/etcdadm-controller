/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestValidateCreate(t *testing.T) {
	cases := map[string]struct {
		in        *EtcdadmCluster
		expectErr string
	}{
		"valid etcdadm cluster": {
			in: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(3)),
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "",
		},
		"no replicas field": {
			in: &EtcdadmCluster{
				Spec:   EtcdadmClusterSpec{},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "spec.replicas: Required value: is required",
		},
		"zero replicas": {
			in: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(0)),
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "cannot be less than or equal to 0",
		},
		"even replicas": {
			in: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(2)),
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "Forbidden: etcd cluster cannot have an even number of nodes",
		},
		"mismatched namespace": {
			in: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(3)),
					InfrastructureTemplate: corev1.ObjectReference{
						Namespace: "fail",
					},
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "Invalid value: \"fail\": must match metadata.namespace",
		},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := tt.in.ValidateCreate()
			if tt.expectErr == "" {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tt.expectErr)))
			}
		})
	}
}
func TestValidateUpdate(t *testing.T) {
	cases := map[string]struct {
		oldConf   *EtcdadmCluster
		newConf   *EtcdadmCluster
		expectErr string
	}{
		"valid scale up": {
			oldConf: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(3)),
				},
				Status: EtcdadmClusterStatus{},
			},
			newConf: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(5)),
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "",
		},
		"valid scale down": {
			oldConf: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(3)),
				},
				Status: EtcdadmClusterStatus{},
			},
			newConf: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(1)),
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "",
		},
		"zero replicas": {
			oldConf: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(3)),
				},
				Status: EtcdadmClusterStatus{},
			},
			newConf: &EtcdadmCluster{
				Spec: EtcdadmClusterSpec{
					Replicas: ptr.To(int32(0)),
				},
				Status: EtcdadmClusterStatus{},
			},
			expectErr: "cannot be less than or equal to 0",
		},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := tt.newConf.ValidateUpdate(tt.oldConf)
			if tt.expectErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.expectErr)))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
