/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

#include "src/stirling/source_connectors/socket_tracer/protocols/redis/parse.h"

#include <initializer_list>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

#include <absl/container/flat_hash_map.h>

#include "src/common/base/base.h"
#include "src/stirling/source_connectors/socket_tracer/protocols/redis/formatting.h"
#include "src/stirling/source_connectors/socket_tracer/protocols/redis/types.h"
#include "src/stirling/utils/binary_decoder.h"

namespace px {
namespace stirling {
namespace protocols {
namespace redis {

namespace {

constexpr char kSimpleStringMarker = '+';
constexpr char kErrorMarker = '-';
constexpr char kIntegerMarker = ':';
constexpr char kBulkStringsMarker = '$';
constexpr char kArrayMarker = '*';
constexpr char kNullMarker = '_';
constexpr char kBooleanMarker = '#';
constexpr char kDoubleMarker = ',';
constexpr char kBigNumberMarker = '(';
constexpr char kVerbatimStringMarker = '=';
constexpr char kBlobErrorMarker = '!';
constexpr char kMapMarker = '%';
constexpr char kSetMarker = '~';
constexpr char kAttributeMarker = '|';
constexpr char kPushMarker = '>';
constexpr std::string_view kTypeMarkers = "+-:$*_#,(=!%~|>";
constexpr std::string_view kNullPayload = "<NULL>";
// This is Redis' universal terminating sequence.
constexpr std::string_view kTerminalSequence = "\r\n";
constexpr int kNullSize = -1;

StatusOr<int> ParseSize(BinaryDecoder* decoder) {
  PX_ASSIGN_OR_RETURN(std::string_view size_str, decoder->ExtractStringUntil(kTerminalSequence));

  constexpr size_t kSizeStrMaxLen = 16;
  if (size_str.size() > kSizeStrMaxLen) {
    return error::InvalidArgument(
        "Redis size string is longer than $0, which indicates traffic is misclassified as Redis.",
        kSizeStrMaxLen);
  }

  // Length could be -1, which stands for NULL, and means the value is not set.
  // That's different than an empty string, which length is 0.
  // So here we initialize the value to -2.
  int size = -2;
  if (!absl::SimpleAtoi(size_str, &size)) {
    return error::InvalidArgument("String '$0' cannot be parsed as integer", size_str);
  }

  if (size < kNullSize) {
    return error::InvalidArgument("Size cannot be less than $0, got '$1'", kNullSize, size_str);
  }

  return size;
}

// Bulk string is formatted as <length>\r\n<actual string, up to 512MB>\r\n
Status ParseBulkString(BinaryDecoder* decoder, Message* msg) {
  PX_ASSIGN_OR_RETURN(int len, ParseSize(decoder));

  constexpr int kMaxLen = 512 * 1024 * 1024;
  if (len > kMaxLen) {
    return error::InvalidArgument("Length cannot be larger than 512MB, got '$0'", len);
  }

  if (len == kNullSize) {
    // TODO(yzhao): This appears wrong, as Redis has NULL value, here "<NULL>" is presented as
    // a string. ATM don't know how to output NULL value in Rapidjson. Research and update this.
    msg->payload = kNullPayload;
    return Status::OK();
  }

  PX_ASSIGN_OR_RETURN(std::string_view payload,
                      decoder->ExtractString(len + kTerminalSequence.size()));
  if (!absl::EndsWith(payload, kTerminalSequence)) {
    return error::InvalidArgument("Bulk string should be terminated by '$0'", kTerminalSequence);
  }
  payload.remove_suffix(kTerminalSequence.size());
  msg->payload = payload;
  return Status::OK();
}

bool IsPubMsg(const std::vector<std::string>& payloads) {
  // Published message format is at https://redis.io/topics/pubsub#format-of-pushed-messages
  constexpr size_t kArrayPayloadSize = 3;
  if (payloads.size() < kArrayPayloadSize) {
    return false;
  }
  constexpr std::string_view kMessageStr = "MESSAGE";
  if (absl::AsciiStrToUpper(payloads.front()) != kMessageStr) {
    return false;
  }
  return true;
}

// This calls ParseMessage(), which eventually calls ParseArray() and are both recursive
// functions. This is because Array message can include nested array messages.
Status ParseArray(message_type_t type, BinaryDecoder* decoder, Message* msg,
                  int elements_per_entry);

Status ParseMessage(message_type_t type, BinaryDecoder* decoder, Message* msg) {
  PX_ASSIGN_OR_RETURN(const char type_marker, decoder->ExtractChar());

  switch (type_marker) {
    case kSimpleStringMarker:
    case kDoubleMarker:
    case kBigNumberMarker: {
      PX_ASSIGN_OR_RETURN(std::string_view str, decoder->ExtractStringUntil(kTerminalSequence));
      msg->payload = str;
      break;
    }
    case kNullMarker: {
      PX_ASSIGN_OR_RETURN(std::string_view str, decoder->ExtractStringUntil(kTerminalSequence));
      if (!str.empty()) {
        return error::InvalidArgument("Null should have no payload, got '$0'", str);
      }
      msg->payload = kNullPayload;
      break;
    }
    case kBooleanMarker: {
      PX_ASSIGN_OR_RETURN(std::string_view str, decoder->ExtractStringUntil(kTerminalSequence));
      if (str != "t" && str != "f") {
        return error::InvalidArgument("Boolean should be 't' or 'f', got '$0'", str);
      }
      msg->payload = str == "t" ? "true" : "false";
      break;
    }
    case kBulkStringsMarker: {
      PX_RETURN_IF_ERROR(ParseBulkString(decoder, msg));
      break;
    }
    case kVerbatimStringMarker: {
      PX_RETURN_IF_ERROR(ParseBulkString(decoder, msg));
      constexpr size_t kFormatPrefixSize = 4;
      if (msg->payload.size() < kFormatPrefixSize || msg->payload[kFormatPrefixSize - 1] != ':') {
        return error::InvalidArgument("Verbatim string should start with '<format>:'");
      }
      msg->payload.erase(0, kFormatPrefixSize);
      break;
    }
    case kBlobErrorMarker: {
      PX_RETURN_IF_ERROR(ParseBulkString(decoder, msg));
      msg->payload = absl::StrCat("-", msg->payload);
      break;
    }
    case kErrorMarker: {
      PX_ASSIGN_OR_RETURN(std::string_view str, decoder->ExtractStringUntil(kTerminalSequence));
      // Append ErrorMarker in front to differentiate error messages from the rest.
      msg->payload = absl::StrCat("-", str);
      break;
    }
    case kIntegerMarker: {
      PX_ASSIGN_OR_RETURN(msg->payload, decoder->ExtractStringUntil(kTerminalSequence));
      break;
    }
    case kArrayMarker:
    case kSetMarker:
    case kPushMarker: {
      PX_RETURN_IF_ERROR(ParseArray(type, decoder, msg, 1));
      break;
    }
    case kMapMarker: {
      PX_RETURN_IF_ERROR(ParseArray(type, decoder, msg, 2));
      break;
    }
    case kAttributeMarker: {
      Message attributes;
      PX_RETURN_IF_ERROR(ParseArray(type, decoder, &attributes, 2));
      PX_RETURN_IF_ERROR(ParseMessage(type, decoder, msg));
      break;
    }
    default:
      return error::InvalidArgument("Unexpected Redis type marker char (displayed as integer): %d",
                                    type_marker);
  }
  // This is needed for GCC build.
  return Status::OK();
}

// Array is formatted as *<size_str>\r\n[one of simple string, error, bulk string, etc.]
Status ParseArray(message_type_t type, BinaryDecoder* decoder, Message* msg,
                  int elements_per_entry) {
  PX_ASSIGN_OR_RETURN(int len, ParseSize(decoder));

  if (len == kNullSize) {
    constexpr std::string_view kNullArray = "[NULL]";
    msg->payload = kNullArray;
    return Status::OK();
  }

  std::vector<std::string> payloads;
  for (int i = 0; i < len * elements_per_entry; ++i) {
    Message tmp;
    PX_RETURN_IF_ERROR(ParseMessage(type, decoder, &tmp));
    payloads.push_back(std::move(tmp.payload));
  }

  FormatArrayMessage(VectorView<std::string>(payloads), msg);

  if (type == message_type_t::kResponse && IsPubMsg(payloads)) {
    msg->is_published_message = true;
  }

  return Status::OK();
}

}  // namespace

size_t FindMessageBoundary(std::string_view buf, size_t start_pos) {
  return buf.find_first_of(kTypeMarkers, start_pos);
}

// Redis protocol specification: https://redis.io/topics/protocol
// This can also be implemented as a recursive function.
ParseState ParseMessage(message_type_t type, std::string_view* buf, Message* msg) {
  BinaryDecoder decoder(*buf);

  auto status = ParseMessage(type, &decoder, msg);

  if (!status.ok()) {
    return TranslateStatus(status);
  }

  *buf = decoder.Buf();

  return ParseState::kSuccess;
}

}  // namespace redis

template <>
size_t FindFrameBoundary<redis::Message>(message_type_t /*type*/, std::string_view buf,
                                         size_t start_pos, NoState* /*state*/) {
  return redis::FindMessageBoundary(buf, start_pos);
}

template <>
ParseState ParseFrame(message_type_t type, std::string_view* buf, redis::Message* msg,
                      NoState* /*state*/) {
  return redis::ParseMessage(type, buf, msg);
}

}  // namespace protocols
}  // namespace stirling
}  // namespace px
