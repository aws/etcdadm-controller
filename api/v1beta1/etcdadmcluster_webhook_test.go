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
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestEtcdadmClusterDefaultCastFail(t *testing.T) {
	g := NewWithT(t)
	
	// Create a different type that will cause the cast to fail
	wrongType := &runtime.Unknown{}
	
	// Create the config object that implements CustomDefaulter
	config := &EtcdadmCluster{}
	
	// Call Default with the wrong type
	err := config.Default(context.TODO(), wrongType)
	
	// Verify that an error is returned
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("expected an EtcdadmCluster"))
}

func TestEtcdadmClusterValidateCreateCastFail(t *testing.T) {
	g := NewWithT(t)
	
	// Create a different type that will cause the cast to fail
	wrongType := &runtime.Unknown{}
	
	// Create the config object that implements CustomValidator
	config := &EtcdadmCluster{}
	
	// Call ValidateCreate with the wrong type
	warnings, err := config.ValidateCreate(context.TODO(), wrongType)
	
	// Verify that an error is returned
	g.Expect(warnings).To(BeNil())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("expected an EtcdadmCluster"))
}

func TestEtcdadmClusterValidateUpdateCastFail(t *testing.T) {
	g := NewWithT(t)
	
	// Create a different type that will cause the cast to fail
	wrongType := &runtime.Unknown{}
	
	// Create the config object that implements CustomValidator
	config := &EtcdadmCluster{}
	
	// Call ValidateUpdate with the wrong type
	warnings, err := config.ValidateUpdate(context.TODO(), wrongType, &EtcdadmCluster{})
	
	// Verify that an error is returned
	g.Expect(warnings).To(BeNil())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("expected an EtcdadmCluster"))
}

func TestEtcdadmClusterValidateDeleteCastFail(t *testing.T) {
	g := NewWithT(t)
	
	// Create a different type that will cause the cast to fail
	wrongType := &runtime.Unknown{}
	
	// Create the config object that implements CustomValidator
	config := &EtcdadmCluster{}
	
	// Call ValidateDelete with the wrong type
	warnings, err := config.ValidateDelete(context.TODO(), wrongType)
	
	// Verify that an error is returned
	g.Expect(warnings).To(BeNil())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("expected an EtcdadmCluster"))
}