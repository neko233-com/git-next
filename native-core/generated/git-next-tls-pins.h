/* Generated public provider checksums. SPDX-License-Identifier: GPL-2.0-only */
struct git_next_tls_pin { const char *name; const char *sha256; };
#if defined(_M_ARM64) || defined(__aarch64__)
#define GIT_NEXT_TLS_PLATFORM "windows-arm64"
#define GIT_NEXT_TLS_ARCHIVE_SHA256 "ce10b24c74ac9c724ab81e2ee30d06e7ee693977a552b8da4e434e909a641847"
#define GIT_NEXT_TLS_ENTRYPOINT "clangarm64/bin/libcurl-openssl-4.dll"
#define GIT_NEXT_TLS_NATIVE_PREFIX "clangarm64"
static const struct git_next_tls_pin git_next_tls_pins[] = {
	{ "clangarm64/bin/libbrotlicommon.dll", "6f2ab8fa291bfa9b3c8fd335a68c17094ee1f72db313e4ffe1f598a375126a7e" },
	{ "clangarm64/bin/libbrotlidec.dll", "916b14535c4cedd0edb53c6a3dc883f1223b8a378ec4838353b37f36cbf23822" },
	{ "clangarm64/bin/libcrypto-3.dll", "d72c7200b655a89111d2a243a1ed3c834fadac4a66ba8affaef6e327662217a9" },
	{ "clangarm64/bin/libcurl-openssl-4.dll", "cf206c3fbabc8eb05abac14474be2454f75576f9ccd3439074653f20ab1bbf2f" },
	{ "clangarm64/bin/libiconv-2.dll", "5eb3e024adbeb40e990530b64498fa5305b4ebeaa34db971196f92317f70a35d" },
	{ "clangarm64/bin/libidn2-0.dll", "0e3d9e52a20dcd7a6941413932f938893f1ed791dd0e09613b870803971783e9" },
	{ "clangarm64/bin/libintl-8.dll", "31db0d0e7780cf28dca1309a894cb775b2ab130c4ba777c546a206970ca47320" },
	{ "clangarm64/bin/libnghttp2-14.dll", "b1f04dfeb31d79d27116a378d766ea7cadc2f9932498400d65dfe3a78213c922" },
	{ "clangarm64/bin/libpsl-5.dll", "95f5a1cac819829e39e21eea8b3b95ab5aa14fb4fdab8cc95b963577adea411f" },
	{ "clangarm64/bin/libssh2-1.dll", "8a8ce51fc45b2e4dcd9b48ec79ccc7ed11dda68d713c54ca466600a041151a1c" },
	{ "clangarm64/bin/libssl-3.dll", "99c1ff9e9d04c9ec201fafe18bb864cc7d91f3c96c10791028bd6edb16a547e3" },
	{ "clangarm64/bin/libunistring-5.dll", "57b8683ebef6a86c46ffd1d0e691079d9002b14244bbded122d50c3257361b78" },
	{ "clangarm64/bin/libzstd.dll", "8c1a2c1e81d37f916192f6484854cb652c5ed1ce9e3ce74b6a746d875e4b7a42" },
	{ "clangarm64/bin/zlib1.dll", "fed18d2fda0a1b0252787fd59e7c01ed900f5c90eeb131f30a9992abb328c7ca" },
	{ "clangarm64/etc/ssl/openssl.cnf", "a65a2cb9f4ee8ffdc7ef4f0ac600c0bdafb95b7b1ab457188ac610a62f5ad6b3" },
	{ "clangarm64/etc/ssl/openssl.cnf.dist", "a65a2cb9f4ee8ffdc7ef4f0ac600c0bdafb95b7b1ab457188ac610a62f5ad6b3" },
	{ "clangarm64/lib/ossl-modules/legacy.dll", "bc8d849cf25c30fd532177e7a7e87be4c2f56efdb89b43989be9801d23bb667c" },
};
#else
#define GIT_NEXT_TLS_PLATFORM "windows-x64"
#define GIT_NEXT_TLS_ARCHIVE_SHA256 "e1819cee60d09793dde322cdb1170e03663c41cd9265cf45246219fc5e6aeecd"
#define GIT_NEXT_TLS_ENTRYPOINT "mingw64/bin/libcurl-openssl-4.dll"
#define GIT_NEXT_TLS_NATIVE_PREFIX "mingw64"
static const struct git_next_tls_pin git_next_tls_pins[] = {
	{ "mingw64/bin/libbrotlicommon.dll", "ca7bc06ead80259971badf2cd0bfc688db9154dad65f139f49a38b4be9976f97" },
	{ "mingw64/bin/libbrotlidec.dll", "a142dc6cfa77cb907028aa9426f486f3d8de37855e8db2ae2894147908121881" },
	{ "mingw64/bin/libcrypto-3-x64.dll", "ef8dc7e412a064e8bdc9243f8b71dcece44d837794047a69f80b491df8ed25e6" },
	{ "mingw64/bin/libcurl-openssl-4.dll", "5ac8dd34e2cbd11faf06d4058a34a074511883355f8855fc066cc63302579358" },
	{ "mingw64/bin/libiconv-2.dll", "7a282a854e01be726c6cccfe46f548c716aa45b3014818468253aaa4efbcd067" },
	{ "mingw64/bin/libidn2-0.dll", "9880b52651a0247640a2531d777380f7c35fac7efa7e3979b265ddcb78db8642" },
	{ "mingw64/bin/libintl-8.dll", "0537c3dd2378218508ebe3cc416d72a99ee2d24ae1c5525e23458f32544ef861" },
	{ "mingw64/bin/libnghttp2-14.dll", "edb721b9d797e64900449d4e5d114536bd2f432e5ec695919f9af8555ff6fe1e" },
	{ "mingw64/bin/libpsl-5.dll", "b31fc70eda598a39b65fe2736d815518db0652d641346f75eac40c2a0499b058" },
	{ "mingw64/bin/libssh2-1.dll", "ee7fe3e749859334c99e9261689481dd879811122598d22585421f95d29fc7ae" },
	{ "mingw64/bin/libssl-3-x64.dll", "3f73db31036a78cc7f0749c199c8ca594c947ab991b9daed646d73729d1f5c4a" },
	{ "mingw64/bin/libunistring-5.dll", "70813b4aaa82f8b0999d8c2fdd4c49c6d31035f17325f5f138f39f7273449adf" },
	{ "mingw64/bin/libzstd.dll", "b13d4f30b93c96823473d742dda0075f7334cba03a40c33d8a7dc282e37b1500" },
	{ "mingw64/bin/zlib1.dll", "93e9243a44c29200eeacaf9658efe2558581770e4b11ca4b500e18e424a6e3b5" },
	{ "mingw64/etc/ssl/openssl.cnf", "a65a2cb9f4ee8ffdc7ef4f0ac600c0bdafb95b7b1ab457188ac610a62f5ad6b3" },
	{ "mingw64/etc/ssl/openssl.cnf.dist", "a65a2cb9f4ee8ffdc7ef4f0ac600c0bdafb95b7b1ab457188ac610a62f5ad6b3" },
	{ "mingw64/lib/ossl-modules/legacy.dll", "3b7b3b535158fa2a317ac45f49c78882ebfaf7ecf21fd43882bf12f52bb6f79e" },
};
#endif
