# Go TLS fixtures

`test_go_tls_1_23_binary` and `test_go_tls_1_24_binary` are built by bazel through
`go_cross_binary` over `pl_supported_go_sdk_versions`.

`test_go_tls_1_25_binary` and `test_go_tls_1_26_binary` are prebuilt, because the bazel
Go SDK list does not include Go 1.25 or 1.26 yet. Once those SDKs are added to
`pl_supported_go_sdk_versions`, delete the prebuilt files and `build_prebuilt.sh`.

To reproduce the prebuilt files:

```
# At the top of the repo.
src/stirling/obj_tools/testdata/go/tls_fixture/build_prebuilt.sh 1.25.8 1.26.7
```

The `*_stripped_binary` targets are produced from each fixture with `objcopy --strip-all`.
