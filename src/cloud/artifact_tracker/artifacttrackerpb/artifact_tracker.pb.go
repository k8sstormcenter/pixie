// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: src/cloud/artifact_tracker/artifacttrackerpb/artifact_tracker.proto

package artifacttrackerpb

import (
	context "context"
	fmt "fmt"
	_ "github.com/gogo/protobuf/gogoproto"
	proto "github.com/gogo/protobuf/proto"
	types "github.com/gogo/protobuf/types"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	io "io"
	math "math"
	math_bits "math/bits"
	versionspb "px.dev/pixie/src/shared/artifacts/versionspb"
	reflect "reflect"
	strings "strings"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

type GetArtifactListRequest struct {
	ArtifactName string                  `protobuf:"bytes,1,opt,name=artifact_name,json=artifactName,proto3" json:"artifact_name,omitempty"`
	ArtifactType versionspb.ArtifactType `protobuf:"varint,2,opt,name=artifact_type,json=artifactType,proto3,enum=px.versions.ArtifactType" json:"artifact_type,omitempty"`
	Limit        int64                   `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (m *GetArtifactListRequest) Reset()      { *m = GetArtifactListRequest{} }
func (*GetArtifactListRequest) ProtoMessage() {}
func (*GetArtifactListRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_6fba5f49ea413862, []int{0}
}
func (m *GetArtifactListRequest) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *GetArtifactListRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_GetArtifactListRequest.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *GetArtifactListRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_GetArtifactListRequest.Merge(m, src)
}
func (m *GetArtifactListRequest) XXX_Size() int {
	return m.Size()
}
func (m *GetArtifactListRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_GetArtifactListRequest.DiscardUnknown(m)
}

var xxx_messageInfo_GetArtifactListRequest proto.InternalMessageInfo

func (m *GetArtifactListRequest) GetArtifactName() string {
	if m != nil {
		return m.ArtifactName
	}
	return ""
}

func (m *GetArtifactListRequest) GetArtifactType() versionspb.ArtifactType {
	if m != nil {
		return m.ArtifactType
	}
	return versionspb.AT_UNKNOWN
}

func (m *GetArtifactListRequest) GetLimit() int64 {
	if m != nil {
		return m.Limit
	}
	return 0
}

type GetDownloadLinkRequest struct {
	ArtifactName string                  `protobuf:"bytes,1,opt,name=artifact_name,json=artifactName,proto3" json:"artifact_name,omitempty"`
	VersionStr   string                  `protobuf:"bytes,2,opt,name=version_str,json=versionStr,proto3" json:"version_str,omitempty"`
	ArtifactType versionspb.ArtifactType `protobuf:"varint,3,opt,name=artifact_type,json=artifactType,proto3,enum=px.versions.ArtifactType" json:"artifact_type,omitempty"`
}

func (m *GetDownloadLinkRequest) Reset()      { *m = GetDownloadLinkRequest{} }
func (*GetDownloadLinkRequest) ProtoMessage() {}
func (*GetDownloadLinkRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_6fba5f49ea413862, []int{1}
}
func (m *GetDownloadLinkRequest) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *GetDownloadLinkRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_GetDownloadLinkRequest.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *GetDownloadLinkRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_GetDownloadLinkRequest.Merge(m, src)
}
func (m *GetDownloadLinkRequest) XXX_Size() int {
	return m.Size()
}
func (m *GetDownloadLinkRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_GetDownloadLinkRequest.DiscardUnknown(m)
}

var xxx_messageInfo_GetDownloadLinkRequest proto.InternalMessageInfo

func (m *GetDownloadLinkRequest) GetArtifactName() string {
	if m != nil {
		return m.ArtifactName
	}
	return ""
}

func (m *GetDownloadLinkRequest) GetVersionStr() string {
	if m != nil {
		return m.VersionStr
	}
	return ""
}

func (m *GetDownloadLinkRequest) GetArtifactType() versionspb.ArtifactType {
	if m != nil {
		return m.ArtifactType
	}
	return versionspb.AT_UNKNOWN
}

type GetDownloadLinkResponse struct {
	Url        string           `protobuf:"bytes,1,opt,name=url,proto3" json:"url,omitempty"`
	SHA256     string           `protobuf:"bytes,2,opt,name=sha256,proto3" json:"sha256,omitempty"`
	ValidUntil *types.Timestamp `protobuf:"bytes,3,opt,name=valid_until,json=validUntil,proto3" json:"valid_until,omitempty"`
}

func (m *GetDownloadLinkResponse) Reset()      { *m = GetDownloadLinkResponse{} }
func (*GetDownloadLinkResponse) ProtoMessage() {}
func (*GetDownloadLinkResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_6fba5f49ea413862, []int{2}
}
func (m *GetDownloadLinkResponse) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *GetDownloadLinkResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_GetDownloadLinkResponse.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *GetDownloadLinkResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_GetDownloadLinkResponse.Merge(m, src)
}
func (m *GetDownloadLinkResponse) XXX_Size() int {
	return m.Size()
}
func (m *GetDownloadLinkResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_GetDownloadLinkResponse.DiscardUnknown(m)
}

var xxx_messageInfo_GetDownloadLinkResponse proto.InternalMessageInfo

func (m *GetDownloadLinkResponse) GetUrl() string {
	if m != nil {
		return m.Url
	}
	return ""
}

func (m *GetDownloadLinkResponse) GetSHA256() string {
	if m != nil {
		return m.SHA256
	}
	return ""
}

func (m *GetDownloadLinkResponse) GetValidUntil() *types.Timestamp {
	if m != nil {
		return m.ValidUntil
	}
	return nil
}

func init() {
	proto.RegisterType((*GetArtifactListRequest)(nil), "px.services.GetArtifactListRequest")
	proto.RegisterType((*GetDownloadLinkRequest)(nil), "px.services.GetDownloadLinkRequest")
	proto.RegisterType((*GetDownloadLinkResponse)(nil), "px.services.GetDownloadLinkResponse")
}

func init() {
	proto.RegisterFile("src/cloud/artifact_tracker/artifacttrackerpb/artifact_tracker.proto", fileDescriptor_6fba5f49ea413862)
}

var fileDescriptor_6fba5f49ea413862 = []byte{
	// 479 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x94, 0x93, 0x31, 0x6f, 0xd3, 0x40,
	0x14, 0xc7, 0x7d, 0x44, 0x44, 0xea, 0x05, 0x28, 0x9c, 0x2a, 0x08, 0x1e, 0x2e, 0x51, 0xca, 0x90,
	0xe9, 0x2c, 0x19, 0xb5, 0x0b, 0x12, 0x52, 0x0b, 0x52, 0x19, 0x2a, 0x84, 0x9c, 0xb2, 0x20, 0xa4,
	0xe8, 0xe2, 0x5c, 0xd3, 0x53, 0x6d, 0xdf, 0x71, 0x77, 0x0e, 0xed, 0xc6, 0x07, 0x60, 0x40, 0x62,
	0x67, 0xe6, 0x6b, 0xb0, 0x31, 0x66, 0xec, 0x84, 0x88, 0xb3, 0x30, 0xf6, 0x23, 0x20, 0xfb, 0x1c,
	0x37, 0xc2, 0x11, 0x52, 0xb6, 0xf7, 0xfe, 0xfe, 0xfb, 0xdd, 0xcf, 0xef, 0x7f, 0x86, 0x2f, 0xb4,
	0x0a, 0xbd, 0x30, 0x12, 0xe9, 0xd8, 0xa3, 0xca, 0xf0, 0x53, 0x1a, 0x9a, 0xa1, 0x51, 0x34, 0x3c,
	0x67, 0xaa, 0x12, 0xca, 0x5e, 0x8e, 0x6a, 0x16, 0x22, 0x95, 0x30, 0x02, 0xb5, 0xe4, 0x05, 0xd1,
	0x4c, 0x4d, 0x79, 0xc8, 0xb4, 0xbb, 0x33, 0x11, 0x13, 0x51, 0xe8, 0x5e, 0x5e, 0x59, 0x8b, 0xdb,
	0x99, 0x08, 0x31, 0x89, 0x98, 0x57, 0x74, 0xa3, 0xf4, 0xd4, 0x33, 0x3c, 0x66, 0xda, 0xd0, 0x58,
	0x96, 0x06, 0x92, 0x83, 0xe8, 0x33, 0xaa, 0xd8, 0x0d, 0x89, 0xf6, 0xa6, 0x4c, 0x69, 0x2e, 0x12,
	0x2d, 0x47, 0x55, 0x69, 0xfd, 0xbd, 0xaf, 0x00, 0x3e, 0x3c, 0x62, 0xe6, 0xa0, 0xb4, 0x1e, 0x73,
	0x6d, 0x02, 0xf6, 0x21, 0x65, 0xda, 0xa0, 0x5d, 0x78, 0xb7, 0x02, 0x4d, 0x68, 0xcc, 0xda, 0xa0,
	0x0b, 0xfa, 0x5b, 0xc1, 0x9d, 0xa5, 0xf8, 0x9a, 0xc6, 0x0c, 0x3d, 0x5f, 0x31, 0x99, 0x4b, 0xc9,
	0xda, 0xb7, 0xba, 0xa0, 0x7f, 0xcf, 0x7f, 0x4c, 0xe4, 0x05, 0xa9, 0x8e, 0x5a, 0x4e, 0x3f, 0xb9,
	0x94, 0xec, 0xe6, 0xfd, 0xbc, 0x43, 0x3b, 0xf0, 0x76, 0xc4, 0x63, 0x6e, 0xda, 0x8d, 0x2e, 0xe8,
	0x37, 0x02, 0xdb, 0xf4, 0xbe, 0x59, 0xaa, 0x97, 0xe2, 0x63, 0x12, 0x09, 0x3a, 0x3e, 0xe6, 0xc9,
	0xf9, 0x46, 0x54, 0x1d, 0xd8, 0x2a, 0x0f, 0x1f, 0x6a, 0xa3, 0x0a, 0xa6, 0xad, 0x00, 0x96, 0xd2,
	0xc0, 0xa8, 0x3a, 0x76, 0x63, 0x23, 0xec, 0xde, 0x67, 0x00, 0x1f, 0xd5, 0x00, 0xb5, 0x14, 0x89,
	0x66, 0xe8, 0x3e, 0x6c, 0xa4, 0x2a, 0x2a, 0xb9, 0xf2, 0x12, 0xf5, 0x60, 0x53, 0x9f, 0x51, 0x7f,
	0x6f, 0xdf, 0x92, 0x1c, 0xc2, 0xec, 0x57, 0xa7, 0x39, 0x78, 0x75, 0xe0, 0xef, 0xed, 0x07, 0xe5,
	0x13, 0xf4, 0x0c, 0xb6, 0xa6, 0x34, 0xe2, 0xe3, 0x61, 0x9a, 0x18, 0x1e, 0x15, 0x3c, 0x2d, 0xdf,
	0x25, 0x36, 0x6f, 0xb2, 0xcc, 0x9b, 0x9c, 0x2c, 0xf3, 0x0e, 0x60, 0x61, 0x7f, 0x9b, 0xbb, 0xfd,
	0x1f, 0x00, 0x6e, 0x57, 0xb4, 0xf6, 0x4e, 0xa1, 0x37, 0x70, 0xfb, 0x9f, 0x60, 0xd1, 0x2e, 0x59,
	0xb9, 0x61, 0x64, 0x7d, 0xec, 0x6e, 0x7b, 0xed, 0x0e, 0x06, 0xcc, 0xa0, 0xf7, 0xc5, 0xc4, 0xd5,
	0x6f, 0xae, 0x4f, 0x5c, 0x13, 0x99, 0xfb, 0xe4, 0xff, 0x26, 0xbb, 0xb6, 0xc3, 0xa3, 0xd9, 0x1c,
	0x3b, 0x57, 0x73, 0xec, 0x5c, 0xcf, 0x31, 0xf8, 0x94, 0x61, 0xf0, 0x3d, 0xc3, 0xe0, 0x67, 0x86,
	0xc1, 0x2c, 0xc3, 0xe0, 0x77, 0x86, 0xc1, 0x9f, 0x0c, 0x3b, 0xd7, 0x19, 0x06, 0x5f, 0x16, 0xd8,
	0x99, 0x2d, 0xb0, 0x73, 0xb5, 0xc0, 0xce, 0xbb, 0x07, 0xb5, 0xbf, 0x6b, 0xd4, 0x2c, 0x96, 0xf5,
	0xf4, 0x6f, 0x00, 0x00, 0x00, 0xff, 0xff, 0x44, 0x6c, 0xdd, 0x55, 0x94, 0x03, 0x00, 0x00,
}

func (this *GetArtifactListRequest) Equal(that interface{}) bool {
	if that == nil {
		return this == nil
	}

	that1, ok := that.(*GetArtifactListRequest)
	if !ok {
		that2, ok := that.(GetArtifactListRequest)
		if ok {
			that1 = &that2
		} else {
			return false
		}
	}
	if that1 == nil {
		return this == nil
	} else if this == nil {
		return false
	}
	if this.ArtifactName != that1.ArtifactName {
		return false
	}
	if this.ArtifactType != that1.ArtifactType {
		return false
	}
	if this.Limit != that1.Limit {
		return false
	}
	return true
}
func (this *GetDownloadLinkRequest) Equal(that interface{}) bool {
	if that == nil {
		return this == nil
	}

	that1, ok := that.(*GetDownloadLinkRequest)
	if !ok {
		that2, ok := that.(GetDownloadLinkRequest)
		if ok {
			that1 = &that2
		} else {
			return false
		}
	}
	if that1 == nil {
		return this == nil
	} else if this == nil {
		return false
	}
	if this.ArtifactName != that1.ArtifactName {
		return false
	}
	if this.VersionStr != that1.VersionStr {
		return false
	}
	if this.ArtifactType != that1.ArtifactType {
		return false
	}
	return true
}
func (this *GetDownloadLinkResponse) Equal(that interface{}) bool {
	if that == nil {
		return this == nil
	}

	that1, ok := that.(*GetDownloadLinkResponse)
	if !ok {
		that2, ok := that.(GetDownloadLinkResponse)
		if ok {
			that1 = &that2
		} else {
			return false
		}
	}
	if that1 == nil {
		return this == nil
	} else if this == nil {
		return false
	}
	if this.Url != that1.Url {
		return false
	}
	if this.SHA256 != that1.SHA256 {
		return false
	}
	if !this.ValidUntil.Equal(that1.ValidUntil) {
		return false
	}
	return true
}
func (this *GetArtifactListRequest) GoString() string {
	if this == nil {
		return "nil"
	}
	s := make([]string, 0, 7)
	s = append(s, "&artifacttrackerpb.GetArtifactListRequest{")
	s = append(s, "ArtifactName: "+fmt.Sprintf("%#v", this.ArtifactName)+",\n")
	s = append(s, "ArtifactType: "+fmt.Sprintf("%#v", this.ArtifactType)+",\n")
	s = append(s, "Limit: "+fmt.Sprintf("%#v", this.Limit)+",\n")
	s = append(s, "}")
	return strings.Join(s, "")
}
func (this *GetDownloadLinkRequest) GoString() string {
	if this == nil {
		return "nil"
	}
	s := make([]string, 0, 7)
	s = append(s, "&artifacttrackerpb.GetDownloadLinkRequest{")
	s = append(s, "ArtifactName: "+fmt.Sprintf("%#v", this.ArtifactName)+",\n")
	s = append(s, "VersionStr: "+fmt.Sprintf("%#v", this.VersionStr)+",\n")
	s = append(s, "ArtifactType: "+fmt.Sprintf("%#v", this.ArtifactType)+",\n")
	s = append(s, "}")
	return strings.Join(s, "")
}
func (this *GetDownloadLinkResponse) GoString() string {
	if this == nil {
		return "nil"
	}
	s := make([]string, 0, 7)
	s = append(s, "&artifacttrackerpb.GetDownloadLinkResponse{")
	s = append(s, "Url: "+fmt.Sprintf("%#v", this.Url)+",\n")
	s = append(s, "SHA256: "+fmt.Sprintf("%#v", this.SHA256)+",\n")
	if this.ValidUntil != nil {
		s = append(s, "ValidUntil: "+fmt.Sprintf("%#v", this.ValidUntil)+",\n")
	}
	s = append(s, "}")
	return strings.Join(s, "")
}
func valueToGoStringArtifactTracker(v interface{}, typ string) string {
	rv := reflect.ValueOf(v)
	if rv.IsNil() {
		return "nil"
	}
	pv := reflect.Indirect(rv).Interface()
	return fmt.Sprintf("func(v %v) *%v { return &v } ( %#v )", typ, typ, pv)
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// ArtifactTrackerClient is the client API for ArtifactTracker service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type ArtifactTrackerClient interface {
	GetArtifactList(ctx context.Context, in *GetArtifactListRequest, opts ...grpc.CallOption) (*versionspb.ArtifactSet, error)
	GetDownloadLink(ctx context.Context, in *GetDownloadLinkRequest, opts ...grpc.CallOption) (*GetDownloadLinkResponse, error)
}

type artifactTrackerClient struct {
	cc *grpc.ClientConn
}

func NewArtifactTrackerClient(cc *grpc.ClientConn) ArtifactTrackerClient {
	return &artifactTrackerClient{cc}
}

func (c *artifactTrackerClient) GetArtifactList(ctx context.Context, in *GetArtifactListRequest, opts ...grpc.CallOption) (*versionspb.ArtifactSet, error) {
	out := new(versionspb.ArtifactSet)
	err := c.cc.Invoke(ctx, "/px.services.ArtifactTracker/GetArtifactList", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *artifactTrackerClient) GetDownloadLink(ctx context.Context, in *GetDownloadLinkRequest, opts ...grpc.CallOption) (*GetDownloadLinkResponse, error) {
	out := new(GetDownloadLinkResponse)
	err := c.cc.Invoke(ctx, "/px.services.ArtifactTracker/GetDownloadLink", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ArtifactTrackerServer is the server API for ArtifactTracker service.
type ArtifactTrackerServer interface {
	GetArtifactList(context.Context, *GetArtifactListRequest) (*versionspb.ArtifactSet, error)
	GetDownloadLink(context.Context, *GetDownloadLinkRequest) (*GetDownloadLinkResponse, error)
}

// UnimplementedArtifactTrackerServer can be embedded to have forward compatible implementations.
type UnimplementedArtifactTrackerServer struct {
}

func (*UnimplementedArtifactTrackerServer) GetArtifactList(ctx context.Context, req *GetArtifactListRequest) (*versionspb.ArtifactSet, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetArtifactList not implemented")
}
func (*UnimplementedArtifactTrackerServer) GetDownloadLink(ctx context.Context, req *GetDownloadLinkRequest) (*GetDownloadLinkResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetDownloadLink not implemented")
}

func RegisterArtifactTrackerServer(s *grpc.Server, srv ArtifactTrackerServer) {
	s.RegisterService(&_ArtifactTracker_serviceDesc, srv)
}

func _ArtifactTracker_GetArtifactList_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetArtifactListRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ArtifactTrackerServer).GetArtifactList(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/px.services.ArtifactTracker/GetArtifactList",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ArtifactTrackerServer).GetArtifactList(ctx, req.(*GetArtifactListRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ArtifactTracker_GetDownloadLink_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetDownloadLinkRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ArtifactTrackerServer).GetDownloadLink(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/px.services.ArtifactTracker/GetDownloadLink",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ArtifactTrackerServer).GetDownloadLink(ctx, req.(*GetDownloadLinkRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _ArtifactTracker_serviceDesc = grpc.ServiceDesc{
	ServiceName: "px.services.ArtifactTracker",
	HandlerType: (*ArtifactTrackerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetArtifactList",
			Handler:    _ArtifactTracker_GetArtifactList_Handler,
		},
		{
			MethodName: "GetDownloadLink",
			Handler:    _ArtifactTracker_GetDownloadLink_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "src/cloud/artifact_tracker/artifacttrackerpb/artifact_tracker.proto",
}

func (m *GetArtifactListRequest) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *GetArtifactListRequest) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *GetArtifactListRequest) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.Limit != 0 {
		i = encodeVarintArtifactTracker(dAtA, i, uint64(m.Limit))
		i--
		dAtA[i] = 0x18
	}
	if m.ArtifactType != 0 {
		i = encodeVarintArtifactTracker(dAtA, i, uint64(m.ArtifactType))
		i--
		dAtA[i] = 0x10
	}
	if len(m.ArtifactName) > 0 {
		i -= len(m.ArtifactName)
		copy(dAtA[i:], m.ArtifactName)
		i = encodeVarintArtifactTracker(dAtA, i, uint64(len(m.ArtifactName)))
		i--
		dAtA[i] = 0xa
	}
	return len(dAtA) - i, nil
}

func (m *GetDownloadLinkRequest) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *GetDownloadLinkRequest) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *GetDownloadLinkRequest) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.ArtifactType != 0 {
		i = encodeVarintArtifactTracker(dAtA, i, uint64(m.ArtifactType))
		i--
		dAtA[i] = 0x18
	}
	if len(m.VersionStr) > 0 {
		i -= len(m.VersionStr)
		copy(dAtA[i:], m.VersionStr)
		i = encodeVarintArtifactTracker(dAtA, i, uint64(len(m.VersionStr)))
		i--
		dAtA[i] = 0x12
	}
	if len(m.ArtifactName) > 0 {
		i -= len(m.ArtifactName)
		copy(dAtA[i:], m.ArtifactName)
		i = encodeVarintArtifactTracker(dAtA, i, uint64(len(m.ArtifactName)))
		i--
		dAtA[i] = 0xa
	}
	return len(dAtA) - i, nil
}

func (m *GetDownloadLinkResponse) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *GetDownloadLinkResponse) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *GetDownloadLinkResponse) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.ValidUntil != nil {
		{
			size, err := m.ValidUntil.MarshalToSizedBuffer(dAtA[:i])
			if err != nil {
				return 0, err
			}
			i -= size
			i = encodeVarintArtifactTracker(dAtA, i, uint64(size))
		}
		i--
		dAtA[i] = 0x1a
	}
	if len(m.SHA256) > 0 {
		i -= len(m.SHA256)
		copy(dAtA[i:], m.SHA256)
		i = encodeVarintArtifactTracker(dAtA, i, uint64(len(m.SHA256)))
		i--
		dAtA[i] = 0x12
	}
	if len(m.Url) > 0 {
		i -= len(m.Url)
		copy(dAtA[i:], m.Url)
		i = encodeVarintArtifactTracker(dAtA, i, uint64(len(m.Url)))
		i--
		dAtA[i] = 0xa
	}
	return len(dAtA) - i, nil
}

func encodeVarintArtifactTracker(dAtA []byte, offset int, v uint64) int {
	offset -= sovArtifactTracker(v)
	base := offset
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return base
}
func (m *GetArtifactListRequest) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.ArtifactName)
	if l > 0 {
		n += 1 + l + sovArtifactTracker(uint64(l))
	}
	if m.ArtifactType != 0 {
		n += 1 + sovArtifactTracker(uint64(m.ArtifactType))
	}
	if m.Limit != 0 {
		n += 1 + sovArtifactTracker(uint64(m.Limit))
	}
	return n
}

func (m *GetDownloadLinkRequest) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.ArtifactName)
	if l > 0 {
		n += 1 + l + sovArtifactTracker(uint64(l))
	}
	l = len(m.VersionStr)
	if l > 0 {
		n += 1 + l + sovArtifactTracker(uint64(l))
	}
	if m.ArtifactType != 0 {
		n += 1 + sovArtifactTracker(uint64(m.ArtifactType))
	}
	return n
}

func (m *GetDownloadLinkResponse) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.Url)
	if l > 0 {
		n += 1 + l + sovArtifactTracker(uint64(l))
	}
	l = len(m.SHA256)
	if l > 0 {
		n += 1 + l + sovArtifactTracker(uint64(l))
	}
	if m.ValidUntil != nil {
		l = m.ValidUntil.Size()
		n += 1 + l + sovArtifactTracker(uint64(l))
	}
	return n
}

func sovArtifactTracker(x uint64) (n int) {
	return (math_bits.Len64(x|1) + 6) / 7
}
func sozArtifactTracker(x uint64) (n int) {
	return sovArtifactTracker(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (this *GetArtifactListRequest) String() string {
	if this == nil {
		return "nil"
	}
	s := strings.Join([]string{`&GetArtifactListRequest{`,
		`ArtifactName:` + fmt.Sprintf("%v", this.ArtifactName) + `,`,
		`ArtifactType:` + fmt.Sprintf("%v", this.ArtifactType) + `,`,
		`Limit:` + fmt.Sprintf("%v", this.Limit) + `,`,
		`}`,
	}, "")
	return s
}
func (this *GetDownloadLinkRequest) String() string {
	if this == nil {
		return "nil"
	}
	s := strings.Join([]string{`&GetDownloadLinkRequest{`,
		`ArtifactName:` + fmt.Sprintf("%v", this.ArtifactName) + `,`,
		`VersionStr:` + fmt.Sprintf("%v", this.VersionStr) + `,`,
		`ArtifactType:` + fmt.Sprintf("%v", this.ArtifactType) + `,`,
		`}`,
	}, "")
	return s
}
func (this *GetDownloadLinkResponse) String() string {
	if this == nil {
		return "nil"
	}
	s := strings.Join([]string{`&GetDownloadLinkResponse{`,
		`Url:` + fmt.Sprintf("%v", this.Url) + `,`,
		`SHA256:` + fmt.Sprintf("%v", this.SHA256) + `,`,
		`ValidUntil:` + strings.Replace(fmt.Sprintf("%v", this.ValidUntil), "Timestamp", "types.Timestamp", 1) + `,`,
		`}`,
	}, "")
	return s
}
func valueToStringArtifactTracker(v interface{}) string {
	rv := reflect.ValueOf(v)
	if rv.IsNil() {
		return "nil"
	}
	pv := reflect.Indirect(rv).Interface()
	return fmt.Sprintf("*%v", pv)
}
func (m *GetArtifactListRequest) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowArtifactTracker
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: GetArtifactListRequest: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: GetArtifactListRequest: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ArtifactName", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.ArtifactName = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field ArtifactType", wireType)
			}
			m.ArtifactType = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.ArtifactType |= versionspb.ArtifactType(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		case 3:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field Limit", wireType)
			}
			m.Limit = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.Limit |= int64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		default:
			iNdEx = preIndex
			skippy, err := skipArtifactTracker(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *GetDownloadLinkRequest) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowArtifactTracker
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: GetDownloadLinkRequest: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: GetDownloadLinkRequest: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ArtifactName", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.ArtifactName = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field VersionStr", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.VersionStr = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 3:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field ArtifactType", wireType)
			}
			m.ArtifactType = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.ArtifactType |= versionspb.ArtifactType(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		default:
			iNdEx = preIndex
			skippy, err := skipArtifactTracker(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *GetDownloadLinkResponse) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowArtifactTracker
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: GetDownloadLinkResponse: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: GetDownloadLinkResponse: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Url", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Url = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field SHA256", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.SHA256 = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ValidUntil", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			if m.ValidUntil == nil {
				m.ValidUntil = &types.Timestamp{}
			}
			if err := m.ValidUntil.Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipArtifactTracker(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthArtifactTracker
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func skipArtifactTracker(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	depth := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowArtifactTracker
			}
			if iNdEx >= l {
				return 0, io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		wireType := int(wire & 0x7)
		switch wireType {
		case 0:
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				iNdEx++
				if dAtA[iNdEx-1] < 0x80 {
					break
				}
			}
		case 1:
			iNdEx += 8
		case 2:
			var length int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowArtifactTracker
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				length |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if length < 0 {
				return 0, ErrInvalidLengthArtifactTracker
			}
			iNdEx += length
		case 3:
			depth++
		case 4:
			if depth == 0 {
				return 0, ErrUnexpectedEndOfGroupArtifactTracker
			}
			depth--
		case 5:
			iNdEx += 4
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
		if iNdEx < 0 {
			return 0, ErrInvalidLengthArtifactTracker
		}
		if depth == 0 {
			return iNdEx, nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

var (
	ErrInvalidLengthArtifactTracker        = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowArtifactTracker          = fmt.Errorf("proto: integer overflow")
	ErrUnexpectedEndOfGroupArtifactTracker = fmt.Errorf("proto: unexpected end of group")
)
