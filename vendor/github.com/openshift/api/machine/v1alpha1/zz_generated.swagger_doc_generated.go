package v1alpha1

// This file contains a collection of methods that can be used from go-restful to
// generate Swagger API documentation for its models. Please read this PR for more
// information on the implementation: https://github.com/emicklei/go-restful/pull/215
//
// TODOs are ignored from the parser (e.g. TODO(andronat):... || TODO:...) if and only if
// they are on one line! For multiple line or blocks that you want to ignore use ---.
// Any context after a --- is ignored.
//
// Those methods can be generated by using hack/update-swagger-docs.sh

// AUTO-GENERATED FUNCTIONS START HERE
var map_AdditionalBlockDevice = map[string]string{
	"":        "additionalBlockDevice is a block device to attach to the server.",
	"name":    "name of the block device in the context of a machine. If the block device is a volume, the Cinder volume will be named as a combination of the machine name and this name. Also, this name will be used for tagging the block device. Information about the block device tag can be obtained from the OpenStack metadata API or the config drive.",
	"sizeGiB": "sizeGiB is the size of the block device in gibibytes (GiB).",
	"storage": "storage specifies the storage type of the block device and additional storage options.",
}

func (AdditionalBlockDevice) SwaggerDoc() map[string]string {
	return map_AdditionalBlockDevice
}

var map_BlockDeviceStorage = map[string]string{
	"":       "blockDeviceStorage is the storage type of a block device to create and contains additional storage options.",
	"type":   "type is the type of block device to create. This can be either \"Volume\" or \"Local\".",
	"volume": "volume contains additional storage options for a volume block device.",
}

func (BlockDeviceStorage) SwaggerDoc() map[string]string {
	return map_BlockDeviceStorage
}

var map_BlockDeviceVolume = map[string]string{
	"":                 "blockDeviceVolume contains additional storage options for a volume block device.",
	"type":             "type is the Cinder volume type of the volume. If omitted, the default Cinder volume type that is configured in the OpenStack cloud will be used.",
	"availabilityZone": "availabilityZone is the volume availability zone to create the volume in. If omitted, the availability zone of the server will be used. The availability zone must NOT contain spaces otherwise it will lead to volume that belongs to this availability zone register failure, see kubernetes/cloud-provider-openstack#1379 for further information.",
}

func (BlockDeviceVolume) SwaggerDoc() map[string]string {
	return map_BlockDeviceVolume
}

var map_Filter = map[string]string{
	"id":           "Deprecated: use NetworkParam.uuid instead. Ignored if NetworkParam.uuid is set.",
	"name":         "name filters networks by name.",
	"description":  "description filters networks by description.",
	"tenantId":     "tenantId filters networks by tenant ID. Deprecated: use projectId instead. tenantId will be ignored if projectId is set.",
	"projectId":    "projectId filters networks by project ID.",
	"tags":         "tags filters by networks containing all specified tags. Multiple tags are comma separated.",
	"tagsAny":      "tagsAny filters by networks containing any specified tags. Multiple tags are comma separated.",
	"notTags":      "notTags filters by networks which don't match all specified tags. NOT (t1 AND t2...) Multiple tags are comma separated.",
	"notTagsAny":   "notTagsAny filters by networks which don't match any specified tags. NOT (t1 OR t2...) Multiple tags are comma separated.",
	"status":       "Deprecated: status is silently ignored. It has no replacement.",
	"adminStateUp": "Deprecated: adminStateUp is silently ignored. It has no replacement.",
	"shared":       "Deprecated: shared is silently ignored. It has no replacement.",
	"marker":       "Deprecated: marker is silently ignored. It has no replacement.",
	"limit":        "Deprecated: limit is silently ignored. It has no replacement.",
	"sortKey":      "Deprecated: sortKey is silently ignored. It has no replacement.",
	"sortDir":      "Deprecated: sortDir is silently ignored. It has no replacement.",
}

func (Filter) SwaggerDoc() map[string]string {
	return map_Filter
}

var map_FixedIPs = map[string]string{
	"subnetID":  "subnetID specifies the ID of the subnet where the fixed IP will be allocated.",
	"ipAddress": "ipAddress is a specific IP address to use in the given subnet. Port creation will fail if the address is not available. If not specified, an available IP from the given subnet will be selected automatically.",
}

func (FixedIPs) SwaggerDoc() map[string]string {
	return map_FixedIPs
}

var map_NetworkParam = map[string]string{
	"uuid":                  "The UUID of the network. Required if you omit the port attribute.",
	"fixedIp":               "A fixed IPv4 address for the NIC. Deprecated: fixedIP is silently ignored. Use subnets instead.",
	"filter":                "Filters for optional network query",
	"subnets":               "Subnet within a network to use",
	"noAllowedAddressPairs": "noAllowedAddressPairs disables creation of allowed address pairs for the network ports",
	"portTags":              "portTags allows users to specify a list of tags to add to ports created in a given network",
	"vnicType":              "The virtual network interface card (vNIC) type that is bound to the neutron port.",
	"profile":               "A dictionary that enables the application running on the specified host to pass and receive virtual network interface (VIF) port-specific information to the plug-in.",
	"portSecurity":          "portSecurity optionally enables or disables security on ports managed by OpenStack",
}

func (NetworkParam) SwaggerDoc() map[string]string {
	return map_NetworkParam
}

var map_OpenstackProviderSpec = map[string]string{
	"":                       "OpenstackProviderSpec is the type that will be embedded in a Machine.Spec.ProviderSpec field for an OpenStack Instance. It is used by the Openstack machine actuator to create a single machine instance. Compatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.",
	"metadata":               "metadata is the standard object's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"cloudsSecret":           "The name of the secret containing the openstack credentials",
	"cloudName":              "The name of the cloud to use from the clouds secret",
	"flavor":                 "The flavor reference for the flavor for your server instance.",
	"image":                  "The name of the image to use for your server instance. If the RootVolume is specified, this will be ignored and use rootVolume directly.",
	"keyName":                "The ssh key to inject in the instance",
	"sshUserName":            "The machine ssh username Deprecated: sshUserName is silently ignored.",
	"networks":               "A networks object. Required parameter when there are multiple networks defined for the tenant. When you do not specify the networks parameter, the server attaches to the only network created for the current tenant.",
	"ports":                  "Create and assign additional ports to instances",
	"floatingIP":             "floatingIP specifies a floating IP to be associated with the machine. Note that it is not safe to use this parameter in a MachineSet, as only one Machine may be assigned the same floating IP.\n\nDeprecated: floatingIP will be removed in a future release as it cannot be implemented correctly.",
	"availabilityZone":       "The availability zone from which to launch the server.",
	"securityGroups":         "The names of the security groups to assign to the instance",
	"userDataSecret":         "The name of the secret containing the user data (startup script in most cases)",
	"trunk":                  "Whether the server instance is created on a trunk port or not.",
	"tags":                   "Machine tags Requires Nova api 2.52 minimum!",
	"serverMetadata":         "Metadata mapping. Allows you to create a map of key value pairs to add to the server instance.",
	"configDrive":            "Config Drive support",
	"rootVolume":             "The volume metadata to boot from",
	"additionalBlockDevices": "additionalBlockDevices is a list of specifications for additional block devices to attach to the server instance",
	"serverGroupID":          "The server group to assign the machine to.",
	"serverGroupName":        "The server group to assign the machine to. A server group with that name will be created if it does not exist. If both ServerGroupID and ServerGroupName are non-empty, they must refer to the same OpenStack resource.",
	"primarySubnet":          "The subnet that a set of machines will get ingress/egress traffic from Deprecated: primarySubnet is silently ignored. Use subnets instead.",
}

func (OpenstackProviderSpec) SwaggerDoc() map[string]string {
	return map_OpenstackProviderSpec
}

var map_PortOpts = map[string]string{
	"networkID":           "networkID is the ID of the network the port will be created in. It is required.",
	"nameSuffix":          "If nameSuffix is specified the created port will be named <machine name>-<nameSuffix>. If not specified the port will be named <machine-name>-<index of this port>.",
	"description":         "description specifies the description of the created port.",
	"adminStateUp":        "adminStateUp sets the administrative state of the created port to up (true), or down (false).",
	"macAddress":          "macAddress specifies the MAC address of the created port.",
	"fixedIPs":            "fixedIPs specifies a set of fixed IPs to assign to the port. They must all be valid for the port's network.",
	"tenantID":            "tenantID specifies the tenant ID of the created port. Note that this requires OpenShift to have administrative permissions, which is typically not the case. Use of this field is not recommended. Deprecated: tenantID is silently ignored.",
	"projectID":           "projectID specifies the project ID of the created port. Note that this requires OpenShift to have administrative permissions, which is typically not the case. Use of this field is not recommended. Deprecated: projectID is silently ignored.",
	"securityGroups":      "securityGroups specifies a set of security group UUIDs to use instead of the machine's default security groups. The default security groups will be used if this is left empty or not specified.",
	"allowedAddressPairs": "allowedAddressPairs specifies a set of allowed address pairs to add to the port.",
	"tags":                "tags species a set of tags to add to the port.",
	"vnicType":            "The virtual network interface card (vNIC) type that is bound to the neutron port.",
	"profile":             "A dictionary that enables the application running on the specified host to pass and receive virtual network interface (VIF) port-specific information to the plug-in.",
	"portSecurity":        "enable or disable security on a given port incompatible with securityGroups and allowedAddressPairs",
	"trunk":               "Enables and disables trunk at port level. If not provided, openStackMachine.Spec.Trunk is inherited.",
	"hostID":              "The ID of the host where the port is allocated. Do not use this field: it cannot be used correctly. Deprecated: hostID is silently ignored. It will be removed with no replacement.",
}

func (PortOpts) SwaggerDoc() map[string]string {
	return map_PortOpts
}

var map_RootVolume = map[string]string{
	"sourceUUID":       "sourceUUID specifies the UUID of a glance image used to populate the root volume. Deprecated: set image in the platform spec instead. This will be ignored if image is set in the platform spec.",
	"volumeType":       "volumeType specifies a volume type to use when creating the root volume. If not specified the default volume type will be used.",
	"diskSize":         "diskSize specifies the size, in GB, of the created root volume.",
	"availabilityZone": "availabilityZone specifies the Cinder availability where the root volume will be created.",
	"sourceType":       "Deprecated: sourceType will be silently ignored. There is no replacement.",
	"deviceType":       "Deprecated: deviceType will be silently ignored. There is no replacement.",
}

func (RootVolume) SwaggerDoc() map[string]string {
	return map_RootVolume
}

var map_SecurityGroupFilter = map[string]string{
	"id":          "id specifies the ID of a security group to use. If set, id will not be validated before use. An invalid id will result in failure to create a server with an appropriate error message.",
	"name":        "name filters security groups by name.",
	"description": "description filters security groups by description.",
	"tenantId":    "tenantId filters security groups by tenant ID. Deprecated: use projectId instead. tenantId will be ignored if projectId is set.",
	"projectId":   "projectId filters security groups by project ID.",
	"tags":        "tags filters by security groups containing all specified tags. Multiple tags are comma separated.",
	"tagsAny":     "tagsAny filters by security groups containing any specified tags. Multiple tags are comma separated.",
	"notTags":     "notTags filters by security groups which don't match all specified tags. NOT (t1 AND t2...) Multiple tags are comma separated.",
	"notTagsAny":  "notTagsAny filters by security groups which don't match any specified tags. NOT (t1 OR t2...) Multiple tags are comma separated.",
	"limit":       "Deprecated: limit is silently ignored. It has no replacement.",
	"marker":      "Deprecated: marker is silently ignored. It has no replacement.",
	"sortKey":     "Deprecated: sortKey is silently ignored. It has no replacement.",
	"sortDir":     "Deprecated: sortDir is silently ignored. It has no replacement.",
}

func (SecurityGroupFilter) SwaggerDoc() map[string]string {
	return map_SecurityGroupFilter
}

var map_SecurityGroupParam = map[string]string{
	"uuid":   "Security Group UUID",
	"name":   "Security Group name",
	"filter": "Filters used to query security groups in openstack",
}

func (SecurityGroupParam) SwaggerDoc() map[string]string {
	return map_SecurityGroupParam
}

var map_SubnetFilter = map[string]string{
	"id":              "id is the uuid of a specific subnet to use. If specified, id will not be validated. Instead server creation will fail with an appropriate error.",
	"name":            "name filters subnets by name.",
	"description":     "description filters subnets by description.",
	"networkId":       "Deprecated: networkId is silently ignored. Set uuid on the containing network definition instead.",
	"tenantId":        "tenantId filters subnets by tenant ID. Deprecated: use projectId instead. tenantId will be ignored if projectId is set.",
	"projectId":       "projectId filters subnets by project ID.",
	"ipVersion":       "ipVersion filters subnets by IP version.",
	"gateway_ip":      "gateway_ip filters subnets by gateway IP.",
	"cidr":            "cidr filters subnets by CIDR.",
	"ipv6AddressMode": "ipv6AddressMode filters subnets by IPv6 address mode.",
	"ipv6RaMode":      "ipv6RaMode filters subnets by IPv6 router adversiement mode.",
	"subnetpoolId":    "subnetpoolId filters subnets by subnet pool ID. Deprecated: subnetpoolId is silently ignored.",
	"tags":            "tags filters by subnets containing all specified tags. Multiple tags are comma separated.",
	"tagsAny":         "tagsAny filters by subnets containing any specified tags. Multiple tags are comma separated.",
	"notTags":         "notTags filters by subnets which don't match all specified tags. NOT (t1 AND t2...) Multiple tags are comma separated.",
	"notTagsAny":      "notTagsAny filters by subnets which don't match any specified tags. NOT (t1 OR t2...) Multiple tags are comma separated.",
	"enableDhcp":      "Deprecated: enableDhcp is silently ignored. It has no replacement.",
	"limit":           "Deprecated: limit is silently ignored. It has no replacement.",
	"marker":          "Deprecated: marker is silently ignored. It has no replacement.",
	"sortKey":         "Deprecated: sortKey is silently ignored. It has no replacement.",
	"sortDir":         "Deprecated: sortDir is silently ignored. It has no replacement.",
}

func (SubnetFilter) SwaggerDoc() map[string]string {
	return map_SubnetFilter
}

var map_SubnetParam = map[string]string{
	"uuid":         "The UUID of the network. Required if you omit the port attribute.",
	"filter":       "Filters for optional network query",
	"portTags":     "portTags are tags that are added to ports created on this subnet",
	"portSecurity": "portSecurity optionally enables or disables security on ports managed by OpenStack Deprecated: portSecurity is silently ignored. Set portSecurity on the parent network instead.",
}

func (SubnetParam) SwaggerDoc() map[string]string {
	return map_SubnetParam
}

// AUTO-GENERATED FUNCTIONS END HERE
