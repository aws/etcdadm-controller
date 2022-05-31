package v1alpha3

import (
	etcdv1beta1 "github.com/aws/etcdadm-controller/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

// ConvertTo converts this EtcdadmCluster to the Hub version (v1beta1).
func (src *EtcdadmCluster) ConvertTo(dstRaw conversion.Hub) error { // nolint
	dst := dstRaw.(*etcdv1beta1.EtcdadmCluster)
	if err := Convert_v1alpha3_EtcdadmCluster_To_v1beta1_EtcdadmCluster(src, dst, nil); err != nil {
		return err
	}
	return nil
}

// ConvertFrom converts from the Hub version (v1beta1) to this EtcdadmCluster.
func (dst *EtcdadmCluster) ConvertFrom(srcRaw conversion.Hub) error { // nolint
	src := srcRaw.(*etcdv1beta1.EtcdadmCluster)
	return Convert_v1beta1_EtcdadmCluster_To_v1alpha3_EtcdadmCluster(src, dst, nil)
}

// ConvertTo converts this EtcdadmClusterList to the Hub version (v1beta1).
func (src *EtcdadmClusterList) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*etcdv1beta1.EtcdadmClusterList)
	if err := Convert_v1alpha3_EtcdadmClusterList_To_v1beta1_EtcdadmClusterList(src, dst, nil); err != nil {
		return err
	}
	return nil
}

// ConvertFrom converts from the Hub version (v1beta1) to this EtcdadmCluster.
func (dst *EtcdadmClusterList) ConvertFrom(srcRaw conversion.Hub) error { // nolint
	src := srcRaw.(*etcdv1beta1.EtcdadmClusterList)
	return Convert_v1beta1_EtcdadmClusterList_To_v1alpha3_EtcdadmClusterList(src, dst, nil)
}
