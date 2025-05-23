// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: src/shared/artifacts/versionspb/versions.proto

package versionspb

import (
	fmt "fmt"
	_ "github.com/gogo/protobuf/gogoproto"
	proto "github.com/gogo/protobuf/proto"
	types "github.com/gogo/protobuf/types"
	io "io"
	math "math"
	math_bits "math/bits"
	reflect "reflect"
	strconv "strconv"
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

type ArtifactType int32

const (
	AT_UNKNOWN                      ArtifactType = 0
	AT_LINUX_AMD64                  ArtifactType = 1
	AT_DARWIN_AMD64                 ArtifactType = 2
	AT_DARWIN_ARM64                 ArtifactType = 3
	AT_CONTAINER_SET_YAMLS          ArtifactType = 50
	AT_CONTAINER_SET_TEMPLATE_YAMLS ArtifactType = 60
	AT_CONTAINER_SET_LINUX_AMD64    ArtifactType = 100
)

var ArtifactType_name = map[int32]string{
	0:   "AT_UNKNOWN",
	1:   "AT_LINUX_AMD64",
	2:   "AT_DARWIN_AMD64",
	3:   "AT_DARWIN_ARM64",
	50:  "AT_CONTAINER_SET_YAMLS",
	60:  "AT_CONTAINER_SET_TEMPLATE_YAMLS",
	100: "AT_CONTAINER_SET_LINUX_AMD64",
}

var ArtifactType_value = map[string]int32{
	"AT_UNKNOWN":                      0,
	"AT_LINUX_AMD64":                  1,
	"AT_DARWIN_AMD64":                 2,
	"AT_DARWIN_ARM64":                 3,
	"AT_CONTAINER_SET_YAMLS":          50,
	"AT_CONTAINER_SET_TEMPLATE_YAMLS": 60,
	"AT_CONTAINER_SET_LINUX_AMD64":    100,
}

func (ArtifactType) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_11101fe785e211c4, []int{0}
}

type ArtifactSet struct {
	Name     string      `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Artifact []*Artifact `protobuf:"bytes,2,rep,name=artifact,proto3" json:"artifact,omitempty"`
}

func (m *ArtifactSet) Reset()      { *m = ArtifactSet{} }
func (*ArtifactSet) ProtoMessage() {}
func (*ArtifactSet) Descriptor() ([]byte, []int) {
	return fileDescriptor_11101fe785e211c4, []int{0}
}
func (m *ArtifactSet) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *ArtifactSet) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_ArtifactSet.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *ArtifactSet) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ArtifactSet.Merge(m, src)
}
func (m *ArtifactSet) XXX_Size() int {
	return m.Size()
}
func (m *ArtifactSet) XXX_DiscardUnknown() {
	xxx_messageInfo_ArtifactSet.DiscardUnknown(m)
}

var xxx_messageInfo_ArtifactSet proto.InternalMessageInfo

func (m *ArtifactSet) GetName() string {
	if m != nil {
		return m.Name
	}
	return ""
}

func (m *ArtifactSet) GetArtifact() []*Artifact {
	if m != nil {
		return m.Artifact
	}
	return nil
}

type ArtifactMirrors struct {
	ArtifactType ArtifactType `protobuf:"varint,1,opt,name=artifact_type,json=artifactType,proto3,enum=px.versions.ArtifactType" json:"artifact_type,omitempty"`
	SHA256       string       `protobuf:"bytes,2,opt,name=sha256,proto3" json:"sha256,omitempty"`
	URLs         []string     `protobuf:"bytes,3,rep,name=urls,proto3" json:"urls,omitempty"`
}

func (m *ArtifactMirrors) Reset()      { *m = ArtifactMirrors{} }
func (*ArtifactMirrors) ProtoMessage() {}
func (*ArtifactMirrors) Descriptor() ([]byte, []int) {
	return fileDescriptor_11101fe785e211c4, []int{1}
}
func (m *ArtifactMirrors) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *ArtifactMirrors) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_ArtifactMirrors.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *ArtifactMirrors) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ArtifactMirrors.Merge(m, src)
}
func (m *ArtifactMirrors) XXX_Size() int {
	return m.Size()
}
func (m *ArtifactMirrors) XXX_DiscardUnknown() {
	xxx_messageInfo_ArtifactMirrors.DiscardUnknown(m)
}

var xxx_messageInfo_ArtifactMirrors proto.InternalMessageInfo

func (m *ArtifactMirrors) GetArtifactType() ArtifactType {
	if m != nil {
		return m.ArtifactType
	}
	return AT_UNKNOWN
}

func (m *ArtifactMirrors) GetSHA256() string {
	if m != nil {
		return m.SHA256
	}
	return ""
}

func (m *ArtifactMirrors) GetURLs() []string {
	if m != nil {
		return m.URLs
	}
	return nil
}

type Artifact struct {
	Timestamp                *types.Timestamp   `protobuf:"bytes,1,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	CommitHash               string             `protobuf:"bytes,2,opt,name=commit_hash,json=commitHash,proto3" json:"commit_hash,omitempty"`
	VersionStr               string             `protobuf:"bytes,3,opt,name=version_str,json=versionStr,proto3" json:"version_str,omitempty"`
	AvailableArtifacts       []ArtifactType     `protobuf:"varint,4,rep,packed,name=available_artifacts,json=availableArtifacts,proto3,enum=px.versions.ArtifactType" json:"available_artifacts,omitempty"` // Deprecated: Do not use.
	Changelog                string             `protobuf:"bytes,5,opt,name=changelog,proto3" json:"changelog,omitempty"`
	AvailableArtifactMirrors []*ArtifactMirrors `protobuf:"bytes,6,rep,name=available_artifact_mirrors,json=availableArtifactMirrors,proto3" json:"available_artifact_mirrors,omitempty"`
}

func (m *Artifact) Reset()      { *m = Artifact{} }
func (*Artifact) ProtoMessage() {}
func (*Artifact) Descriptor() ([]byte, []int) {
	return fileDescriptor_11101fe785e211c4, []int{2}
}
func (m *Artifact) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *Artifact) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_Artifact.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *Artifact) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Artifact.Merge(m, src)
}
func (m *Artifact) XXX_Size() int {
	return m.Size()
}
func (m *Artifact) XXX_DiscardUnknown() {
	xxx_messageInfo_Artifact.DiscardUnknown(m)
}

var xxx_messageInfo_Artifact proto.InternalMessageInfo

func (m *Artifact) GetTimestamp() *types.Timestamp {
	if m != nil {
		return m.Timestamp
	}
	return nil
}

func (m *Artifact) GetCommitHash() string {
	if m != nil {
		return m.CommitHash
	}
	return ""
}

func (m *Artifact) GetVersionStr() string {
	if m != nil {
		return m.VersionStr
	}
	return ""
}

// Deprecated: Do not use.
func (m *Artifact) GetAvailableArtifacts() []ArtifactType {
	if m != nil {
		return m.AvailableArtifacts
	}
	return nil
}

func (m *Artifact) GetChangelog() string {
	if m != nil {
		return m.Changelog
	}
	return ""
}

func (m *Artifact) GetAvailableArtifactMirrors() []*ArtifactMirrors {
	if m != nil {
		return m.AvailableArtifactMirrors
	}
	return nil
}

func init() {
	proto.RegisterEnum("px.versions.ArtifactType", ArtifactType_name, ArtifactType_value)
	proto.RegisterType((*ArtifactSet)(nil), "px.versions.ArtifactSet")
	proto.RegisterType((*ArtifactMirrors)(nil), "px.versions.ArtifactMirrors")
	proto.RegisterType((*Artifact)(nil), "px.versions.Artifact")
}

func init() {
	proto.RegisterFile("src/shared/artifacts/versionspb/versions.proto", fileDescriptor_11101fe785e211c4)
}

var fileDescriptor_11101fe785e211c4 = []byte{
	// 572 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x7c, 0x93, 0xcf, 0x6e, 0xd3, 0x4c,
	0x14, 0xc5, 0x3d, 0x71, 0xbe, 0xa8, 0xb9, 0xe9, 0xd7, 0x46, 0x53, 0x40, 0x26, 0x8a, 0x26, 0x51,
	0xd8, 0x54, 0x2c, 0x6c, 0x61, 0xda, 0x88, 0x05, 0x42, 0x38, 0x34, 0x52, 0x2b, 0x12, 0x17, 0x4d,
	0x5c, 0x15, 0xba, 0x19, 0x4d, 0x52, 0xd7, 0xb6, 0x14, 0xd7, 0x96, 0xc7, 0xad, 0xe8, 0x8e, 0x47,
	0x40, 0xe2, 0x25, 0x78, 0x0b, 0xb6, 0x2c, 0xb3, 0xec, 0xaa, 0xa2, 0xee, 0x86, 0x65, 0x1f, 0x01,
	0xd5, 0x7f, 0xd2, 0x88, 0x54, 0xec, 0xee, 0x9c, 0xfb, 0x1b, 0x9f, 0xe3, 0x7b, 0x6d, 0x50, 0x45,
	0x34, 0xd1, 0x84, 0xcb, 0x23, 0xfb, 0x58, 0xe3, 0x51, 0xec, 0x9d, 0xf0, 0x49, 0x2c, 0xb4, 0x73,
	0x3b, 0x12, 0x5e, 0x70, 0x2a, 0xc2, 0xf1, 0xbc, 0x54, 0xc3, 0x28, 0x88, 0x03, 0x5c, 0x0b, 0x3f,
	0xab, 0x85, 0xd4, 0x78, 0xe4, 0x04, 0x4e, 0x90, 0xea, 0xda, 0x5d, 0x95, 0x21, 0x8d, 0x96, 0x13,
	0x04, 0xce, 0xd4, 0xd6, 0xd2, 0xd3, 0xf8, 0xec, 0x44, 0x8b, 0x3d, 0xdf, 0x16, 0x31, 0xf7, 0xc3,
	0x0c, 0xe8, 0x58, 0x50, 0x33, 0x72, 0xab, 0x91, 0x1d, 0x63, 0x0c, 0xe5, 0x53, 0xee, 0xdb, 0x0a,
	0x6a, 0xa3, 0xcd, 0x2a, 0x4d, 0x6b, 0xfc, 0x02, 0x56, 0x8a, 0x34, 0x4a, 0xa9, 0x2d, 0x6f, 0xd6,
	0xf4, 0xc7, 0xea, 0x82, 0xb3, 0x5a, 0xdc, 0xa7, 0x73, 0xac, 0xf3, 0x0d, 0xc1, 0x7a, 0x21, 0x0f,
	0xbd, 0x28, 0x0a, 0x22, 0x81, 0xdf, 0xc0, 0xff, 0x45, 0x9f, 0xc5, 0x17, 0x61, 0xe6, 0xb1, 0xa6,
	0x3f, 0x7d, 0xf0, 0x59, 0xd6, 0x45, 0x68, 0xd3, 0x55, 0xbe, 0x70, 0xc2, 0x1d, 0xa8, 0x08, 0x97,
	0xeb, 0xdb, 0x5d, 0xa5, 0x74, 0x17, 0xae, 0x07, 0xc9, 0x55, 0xab, 0x32, 0xda, 0x35, 0xf4, 0xed,
	0x2e, 0xcd, 0x3b, 0xb8, 0x09, 0xe5, 0xb3, 0x68, 0x2a, 0x14, 0xb9, 0x2d, 0x6f, 0x56, 0x7b, 0x2b,
	0xc9, 0x55, 0xab, 0x7c, 0x40, 0x07, 0x82, 0xa6, 0x6a, 0x67, 0x56, 0x82, 0x95, 0xc2, 0x00, 0xbf,
	0x82, 0xea, 0x7c, 0x16, 0x69, 0x94, 0x9a, 0xde, 0x50, 0xb3, 0x69, 0xa9, 0xc5, 0xb4, 0x54, 0xab,
	0x20, 0xe8, 0x3d, 0x8c, 0x5b, 0x50, 0x9b, 0x04, 0xbe, 0xef, 0xc5, 0xcc, 0xe5, 0xc2, 0xcd, 0xd2,
	0x50, 0xc8, 0xa4, 0x5d, 0x2e, 0xdc, 0x3b, 0x20, 0x7f, 0x21, 0x26, 0xe2, 0x48, 0x91, 0x33, 0x20,
	0x97, 0x46, 0x71, 0x84, 0x4d, 0xd8, 0xe0, 0xe7, 0xdc, 0x9b, 0xf2, 0xf1, 0xd4, 0x66, 0xf3, 0x4d,
	0x2b, 0xe5, 0xb6, 0xfc, 0xcf, 0x81, 0xf4, 0x4a, 0x0a, 0xa2, 0x78, 0x7e, 0xb3, 0x68, 0x09, 0xdc,
	0x84, 0xea, 0xc4, 0xe5, 0xa7, 0x8e, 0x3d, 0x0d, 0x1c, 0xe5, 0xbf, 0xd4, 0xee, 0x5e, 0xc0, 0x47,
	0xd0, 0x58, 0x76, 0x63, 0x7e, 0xb6, 0x16, 0xa5, 0x92, 0x6e, 0xb4, 0xf9, 0xa0, 0x69, 0xbe, 0x3a,
	0xaa, 0x2c, 0x79, 0xe6, 0x9d, 0xe7, 0x3f, 0x10, 0xac, 0x2e, 0x46, 0xc4, 0x6b, 0x00, 0x86, 0xc5,
	0x0e, 0xcc, 0xf7, 0xe6, 0xfe, 0xa1, 0x59, 0x97, 0x30, 0x86, 0x35, 0xc3, 0x62, 0x83, 0x3d, 0xf3,
	0xe0, 0x23, 0x33, 0x86, 0x3b, 0xdd, 0xad, 0x3a, 0xc2, 0x1b, 0xb0, 0x6e, 0x58, 0x6c, 0xc7, 0xa0,
	0x87, 0x7b, 0x66, 0x2e, 0x96, 0xfe, 0x12, 0xe9, 0xb0, 0xbb, 0x55, 0x97, 0x71, 0x03, 0x9e, 0x18,
	0x16, 0x7b, 0xb7, 0x6f, 0x5a, 0xc6, 0x9e, 0xd9, 0xa7, 0x6c, 0xd4, 0xb7, 0xd8, 0x27, 0x63, 0x38,
	0x18, 0xd5, 0x75, 0xfc, 0x0c, 0x5a, 0x4b, 0x3d, 0xab, 0x3f, 0xfc, 0x30, 0x30, 0xac, 0x7e, 0x0e,
	0xbd, 0xc6, 0x6d, 0x68, 0x2e, 0x41, 0x8b, 0x61, 0x8e, 0x7b, 0x6f, 0x67, 0xd7, 0x44, 0xba, 0xbc,
	0x26, 0xd2, 0xed, 0x35, 0x41, 0x5f, 0x12, 0x82, 0xbe, 0x27, 0x04, 0xfd, 0x4c, 0x08, 0x9a, 0x25,
	0x04, 0xfd, 0x4a, 0x08, 0xfa, 0x9d, 0x10, 0xe9, 0x36, 0x21, 0xe8, 0xeb, 0x0d, 0x91, 0x66, 0x37,
	0x44, 0xba, 0xbc, 0x21, 0xd2, 0x11, 0xdc, 0xff, 0x95, 0xe3, 0x4a, 0xfa, 0xb9, 0xbc, 0xfc, 0x13,
	0x00, 0x00, 0xff, 0xff, 0x22, 0xf7, 0x10, 0x24, 0xbf, 0x03, 0x00, 0x00,
}

func (x ArtifactType) String() string {
	s, ok := ArtifactType_name[int32(x)]
	if ok {
		return s
	}
	return strconv.Itoa(int(x))
}
func (this *ArtifactSet) Equal(that interface{}) bool {
	if that == nil {
		return this == nil
	}

	that1, ok := that.(*ArtifactSet)
	if !ok {
		that2, ok := that.(ArtifactSet)
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
	if this.Name != that1.Name {
		return false
	}
	if len(this.Artifact) != len(that1.Artifact) {
		return false
	}
	for i := range this.Artifact {
		if !this.Artifact[i].Equal(that1.Artifact[i]) {
			return false
		}
	}
	return true
}
func (this *ArtifactMirrors) Equal(that interface{}) bool {
	if that == nil {
		return this == nil
	}

	that1, ok := that.(*ArtifactMirrors)
	if !ok {
		that2, ok := that.(ArtifactMirrors)
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
	if this.ArtifactType != that1.ArtifactType {
		return false
	}
	if this.SHA256 != that1.SHA256 {
		return false
	}
	if len(this.URLs) != len(that1.URLs) {
		return false
	}
	for i := range this.URLs {
		if this.URLs[i] != that1.URLs[i] {
			return false
		}
	}
	return true
}
func (this *Artifact) Equal(that interface{}) bool {
	if that == nil {
		return this == nil
	}

	that1, ok := that.(*Artifact)
	if !ok {
		that2, ok := that.(Artifact)
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
	if !this.Timestamp.Equal(that1.Timestamp) {
		return false
	}
	if this.CommitHash != that1.CommitHash {
		return false
	}
	if this.VersionStr != that1.VersionStr {
		return false
	}
	if len(this.AvailableArtifacts) != len(that1.AvailableArtifacts) {
		return false
	}
	for i := range this.AvailableArtifacts {
		if this.AvailableArtifacts[i] != that1.AvailableArtifacts[i] {
			return false
		}
	}
	if this.Changelog != that1.Changelog {
		return false
	}
	if len(this.AvailableArtifactMirrors) != len(that1.AvailableArtifactMirrors) {
		return false
	}
	for i := range this.AvailableArtifactMirrors {
		if !this.AvailableArtifactMirrors[i].Equal(that1.AvailableArtifactMirrors[i]) {
			return false
		}
	}
	return true
}
func (this *ArtifactSet) GoString() string {
	if this == nil {
		return "nil"
	}
	s := make([]string, 0, 6)
	s = append(s, "&versionspb.ArtifactSet{")
	s = append(s, "Name: "+fmt.Sprintf("%#v", this.Name)+",\n")
	if this.Artifact != nil {
		s = append(s, "Artifact: "+fmt.Sprintf("%#v", this.Artifact)+",\n")
	}
	s = append(s, "}")
	return strings.Join(s, "")
}
func (this *ArtifactMirrors) GoString() string {
	if this == nil {
		return "nil"
	}
	s := make([]string, 0, 7)
	s = append(s, "&versionspb.ArtifactMirrors{")
	s = append(s, "ArtifactType: "+fmt.Sprintf("%#v", this.ArtifactType)+",\n")
	s = append(s, "SHA256: "+fmt.Sprintf("%#v", this.SHA256)+",\n")
	s = append(s, "URLs: "+fmt.Sprintf("%#v", this.URLs)+",\n")
	s = append(s, "}")
	return strings.Join(s, "")
}
func (this *Artifact) GoString() string {
	if this == nil {
		return "nil"
	}
	s := make([]string, 0, 10)
	s = append(s, "&versionspb.Artifact{")
	if this.Timestamp != nil {
		s = append(s, "Timestamp: "+fmt.Sprintf("%#v", this.Timestamp)+",\n")
	}
	s = append(s, "CommitHash: "+fmt.Sprintf("%#v", this.CommitHash)+",\n")
	s = append(s, "VersionStr: "+fmt.Sprintf("%#v", this.VersionStr)+",\n")
	s = append(s, "AvailableArtifacts: "+fmt.Sprintf("%#v", this.AvailableArtifacts)+",\n")
	s = append(s, "Changelog: "+fmt.Sprintf("%#v", this.Changelog)+",\n")
	if this.AvailableArtifactMirrors != nil {
		s = append(s, "AvailableArtifactMirrors: "+fmt.Sprintf("%#v", this.AvailableArtifactMirrors)+",\n")
	}
	s = append(s, "}")
	return strings.Join(s, "")
}
func valueToGoStringVersions(v interface{}, typ string) string {
	rv := reflect.ValueOf(v)
	if rv.IsNil() {
		return "nil"
	}
	pv := reflect.Indirect(rv).Interface()
	return fmt.Sprintf("func(v %v) *%v { return &v } ( %#v )", typ, typ, pv)
}
func (m *ArtifactSet) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *ArtifactSet) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *ArtifactSet) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if len(m.Artifact) > 0 {
		for iNdEx := len(m.Artifact) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.Artifact[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintVersions(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0x12
		}
	}
	if len(m.Name) > 0 {
		i -= len(m.Name)
		copy(dAtA[i:], m.Name)
		i = encodeVarintVersions(dAtA, i, uint64(len(m.Name)))
		i--
		dAtA[i] = 0xa
	}
	return len(dAtA) - i, nil
}

func (m *ArtifactMirrors) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *ArtifactMirrors) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *ArtifactMirrors) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if len(m.URLs) > 0 {
		for iNdEx := len(m.URLs) - 1; iNdEx >= 0; iNdEx-- {
			i -= len(m.URLs[iNdEx])
			copy(dAtA[i:], m.URLs[iNdEx])
			i = encodeVarintVersions(dAtA, i, uint64(len(m.URLs[iNdEx])))
			i--
			dAtA[i] = 0x1a
		}
	}
	if len(m.SHA256) > 0 {
		i -= len(m.SHA256)
		copy(dAtA[i:], m.SHA256)
		i = encodeVarintVersions(dAtA, i, uint64(len(m.SHA256)))
		i--
		dAtA[i] = 0x12
	}
	if m.ArtifactType != 0 {
		i = encodeVarintVersions(dAtA, i, uint64(m.ArtifactType))
		i--
		dAtA[i] = 0x8
	}
	return len(dAtA) - i, nil
}

func (m *Artifact) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *Artifact) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *Artifact) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if len(m.AvailableArtifactMirrors) > 0 {
		for iNdEx := len(m.AvailableArtifactMirrors) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.AvailableArtifactMirrors[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintVersions(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0x32
		}
	}
	if len(m.Changelog) > 0 {
		i -= len(m.Changelog)
		copy(dAtA[i:], m.Changelog)
		i = encodeVarintVersions(dAtA, i, uint64(len(m.Changelog)))
		i--
		dAtA[i] = 0x2a
	}
	if len(m.AvailableArtifacts) > 0 {
		dAtA2 := make([]byte, len(m.AvailableArtifacts)*10)
		var j1 int
		for _, num := range m.AvailableArtifacts {
			for num >= 1<<7 {
				dAtA2[j1] = uint8(uint64(num)&0x7f | 0x80)
				num >>= 7
				j1++
			}
			dAtA2[j1] = uint8(num)
			j1++
		}
		i -= j1
		copy(dAtA[i:], dAtA2[:j1])
		i = encodeVarintVersions(dAtA, i, uint64(j1))
		i--
		dAtA[i] = 0x22
	}
	if len(m.VersionStr) > 0 {
		i -= len(m.VersionStr)
		copy(dAtA[i:], m.VersionStr)
		i = encodeVarintVersions(dAtA, i, uint64(len(m.VersionStr)))
		i--
		dAtA[i] = 0x1a
	}
	if len(m.CommitHash) > 0 {
		i -= len(m.CommitHash)
		copy(dAtA[i:], m.CommitHash)
		i = encodeVarintVersions(dAtA, i, uint64(len(m.CommitHash)))
		i--
		dAtA[i] = 0x12
	}
	if m.Timestamp != nil {
		{
			size, err := m.Timestamp.MarshalToSizedBuffer(dAtA[:i])
			if err != nil {
				return 0, err
			}
			i -= size
			i = encodeVarintVersions(dAtA, i, uint64(size))
		}
		i--
		dAtA[i] = 0xa
	}
	return len(dAtA) - i, nil
}

func encodeVarintVersions(dAtA []byte, offset int, v uint64) int {
	offset -= sovVersions(v)
	base := offset
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return base
}
func (m *ArtifactSet) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.Name)
	if l > 0 {
		n += 1 + l + sovVersions(uint64(l))
	}
	if len(m.Artifact) > 0 {
		for _, e := range m.Artifact {
			l = e.Size()
			n += 1 + l + sovVersions(uint64(l))
		}
	}
	return n
}

func (m *ArtifactMirrors) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if m.ArtifactType != 0 {
		n += 1 + sovVersions(uint64(m.ArtifactType))
	}
	l = len(m.SHA256)
	if l > 0 {
		n += 1 + l + sovVersions(uint64(l))
	}
	if len(m.URLs) > 0 {
		for _, s := range m.URLs {
			l = len(s)
			n += 1 + l + sovVersions(uint64(l))
		}
	}
	return n
}

func (m *Artifact) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if m.Timestamp != nil {
		l = m.Timestamp.Size()
		n += 1 + l + sovVersions(uint64(l))
	}
	l = len(m.CommitHash)
	if l > 0 {
		n += 1 + l + sovVersions(uint64(l))
	}
	l = len(m.VersionStr)
	if l > 0 {
		n += 1 + l + sovVersions(uint64(l))
	}
	if len(m.AvailableArtifacts) > 0 {
		l = 0
		for _, e := range m.AvailableArtifacts {
			l += sovVersions(uint64(e))
		}
		n += 1 + sovVersions(uint64(l)) + l
	}
	l = len(m.Changelog)
	if l > 0 {
		n += 1 + l + sovVersions(uint64(l))
	}
	if len(m.AvailableArtifactMirrors) > 0 {
		for _, e := range m.AvailableArtifactMirrors {
			l = e.Size()
			n += 1 + l + sovVersions(uint64(l))
		}
	}
	return n
}

func sovVersions(x uint64) (n int) {
	return (math_bits.Len64(x|1) + 6) / 7
}
func sozVersions(x uint64) (n int) {
	return sovVersions(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (this *ArtifactSet) String() string {
	if this == nil {
		return "nil"
	}
	repeatedStringForArtifact := "[]*Artifact{"
	for _, f := range this.Artifact {
		repeatedStringForArtifact += strings.Replace(f.String(), "Artifact", "Artifact", 1) + ","
	}
	repeatedStringForArtifact += "}"
	s := strings.Join([]string{`&ArtifactSet{`,
		`Name:` + fmt.Sprintf("%v", this.Name) + `,`,
		`Artifact:` + repeatedStringForArtifact + `,`,
		`}`,
	}, "")
	return s
}
func (this *ArtifactMirrors) String() string {
	if this == nil {
		return "nil"
	}
	s := strings.Join([]string{`&ArtifactMirrors{`,
		`ArtifactType:` + fmt.Sprintf("%v", this.ArtifactType) + `,`,
		`SHA256:` + fmt.Sprintf("%v", this.SHA256) + `,`,
		`URLs:` + fmt.Sprintf("%v", this.URLs) + `,`,
		`}`,
	}, "")
	return s
}
func (this *Artifact) String() string {
	if this == nil {
		return "nil"
	}
	repeatedStringForAvailableArtifactMirrors := "[]*ArtifactMirrors{"
	for _, f := range this.AvailableArtifactMirrors {
		repeatedStringForAvailableArtifactMirrors += strings.Replace(f.String(), "ArtifactMirrors", "ArtifactMirrors", 1) + ","
	}
	repeatedStringForAvailableArtifactMirrors += "}"
	s := strings.Join([]string{`&Artifact{`,
		`Timestamp:` + strings.Replace(fmt.Sprintf("%v", this.Timestamp), "Timestamp", "types.Timestamp", 1) + `,`,
		`CommitHash:` + fmt.Sprintf("%v", this.CommitHash) + `,`,
		`VersionStr:` + fmt.Sprintf("%v", this.VersionStr) + `,`,
		`AvailableArtifacts:` + fmt.Sprintf("%v", this.AvailableArtifacts) + `,`,
		`Changelog:` + fmt.Sprintf("%v", this.Changelog) + `,`,
		`AvailableArtifactMirrors:` + repeatedStringForAvailableArtifactMirrors + `,`,
		`}`,
	}, "")
	return s
}
func valueToStringVersions(v interface{}) string {
	rv := reflect.ValueOf(v)
	if rv.IsNil() {
		return "nil"
	}
	pv := reflect.Indirect(rv).Interface()
	return fmt.Sprintf("*%v", pv)
}
func (m *ArtifactSet) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowVersions
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
			return fmt.Errorf("proto: ArtifactSet: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: ArtifactSet: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Name", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Name = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Artifact", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Artifact = append(m.Artifact, &Artifact{})
			if err := m.Artifact[len(m.Artifact)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipVersions(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthVersions
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
func (m *ArtifactMirrors) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowVersions
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
			return fmt.Errorf("proto: ArtifactMirrors: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: ArtifactMirrors: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field ArtifactType", wireType)
			}
			m.ArtifactType = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.ArtifactType |= ArtifactType(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field SHA256", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.SHA256 = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field URLs", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.URLs = append(m.URLs, string(dAtA[iNdEx:postIndex]))
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipVersions(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthVersions
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
func (m *Artifact) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowVersions
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
			return fmt.Errorf("proto: Artifact: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: Artifact: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Timestamp", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			if m.Timestamp == nil {
				m.Timestamp = &types.Timestamp{}
			}
			if err := m.Timestamp.Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field CommitHash", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.CommitHash = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field VersionStr", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.VersionStr = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 4:
			if wireType == 0 {
				var v ArtifactType
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowVersions
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					v |= ArtifactType(b&0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				m.AvailableArtifacts = append(m.AvailableArtifacts, v)
			} else if wireType == 2 {
				var packedLen int
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowVersions
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					packedLen |= int(b&0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				if packedLen < 0 {
					return ErrInvalidLengthVersions
				}
				postIndex := iNdEx + packedLen
				if postIndex < 0 {
					return ErrInvalidLengthVersions
				}
				if postIndex > l {
					return io.ErrUnexpectedEOF
				}
				var elementCount int
				if elementCount != 0 && len(m.AvailableArtifacts) == 0 {
					m.AvailableArtifacts = make([]ArtifactType, 0, elementCount)
				}
				for iNdEx < postIndex {
					var v ArtifactType
					for shift := uint(0); ; shift += 7 {
						if shift >= 64 {
							return ErrIntOverflowVersions
						}
						if iNdEx >= l {
							return io.ErrUnexpectedEOF
						}
						b := dAtA[iNdEx]
						iNdEx++
						v |= ArtifactType(b&0x7F) << shift
						if b < 0x80 {
							break
						}
					}
					m.AvailableArtifacts = append(m.AvailableArtifacts, v)
				}
			} else {
				return fmt.Errorf("proto: wrong wireType = %d for field AvailableArtifacts", wireType)
			}
		case 5:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Changelog", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Changelog = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 6:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field AvailableArtifactMirrors", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowVersions
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
				return ErrInvalidLengthVersions
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthVersions
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.AvailableArtifactMirrors = append(m.AvailableArtifactMirrors, &ArtifactMirrors{})
			if err := m.AvailableArtifactMirrors[len(m.AvailableArtifactMirrors)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipVersions(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthVersions
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
func skipVersions(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	depth := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowVersions
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
					return 0, ErrIntOverflowVersions
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
					return 0, ErrIntOverflowVersions
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
				return 0, ErrInvalidLengthVersions
			}
			iNdEx += length
		case 3:
			depth++
		case 4:
			if depth == 0 {
				return 0, ErrUnexpectedEndOfGroupVersions
			}
			depth--
		case 5:
			iNdEx += 4
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
		if iNdEx < 0 {
			return 0, ErrInvalidLengthVersions
		}
		if depth == 0 {
			return iNdEx, nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

var (
	ErrInvalidLengthVersions        = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowVersions          = fmt.Errorf("proto: integer overflow")
	ErrUnexpectedEndOfGroupVersions = fmt.Errorf("proto: unexpected end of group")
)
