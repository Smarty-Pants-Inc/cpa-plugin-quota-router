package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const abiBoundarySource = `
#define _POSIX_C_SOURCE 200809L
#include <dlfcn.h>
#include <limits.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; host_call_fn call; free_fn free_buffer; } host_api;
typedef int (*plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*shutdown_fn)(void);
typedef struct { uint32_t abi_version; plugin_call_fn call; free_fn free_buffer; shutdown_fn shutdown; } plugin_api;
typedef int (*init_fn)(host_api*, plugin_api*);

static _Atomic int response_mode = 1;
static _Atomic int oversized_freed;
static _Atomic int malformed_freed;

static int copy_response(cliproxy_buffer* response, const char* text, size_t len) {
	response->ptr = malloc(len);
	if (response->ptr == NULL) return 1;
	memcpy(response->ptr, text, len);
	response->len = len;
	return 0;
}

static int host_call(void* ctx, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	(void)ctx; (void)request; (void)request_len;
	if (method == NULL || response == NULL) return 1;
	response->ptr = NULL;
	response->len = 0;
	if (strcmp(method, "host.auth.list") == 0) {
		if (atomic_load(&response_mode) == 1) {
			response->ptr = malloc(1);
			if (response->ptr == NULL) return 1;
			response->len = (size_t)INT_MAX + 1;
			return 0;
		}
		return copy_response(response, "bad", 3);
	}
	const char ok[] = "{\"ok\":true,\"result\":{}}";
	return copy_response(response, ok, sizeof(ok) - 1);
}

static void host_free(void* ptr, size_t len) {
	if (len > INT_MAX) atomic_store(&oversized_freed, 1);
	if (len == 3) atomic_store(&malformed_freed, 1);
	free(ptr);
}

static int wait_flag(_Atomic int* flag) {
	struct timespec delay = {0, 10000000};
	for (int i = 0; i < 200; i++) {
		if (atomic_load(flag)) return 1;
		nanosleep(&delay, NULL);
	}
	return 0;
}

static void free_plugin_response(plugin_api* plugin, cliproxy_buffer* response) {
	if (response->ptr != NULL) plugin->free_buffer(response->ptr, response->len);
	response->ptr = NULL;
	response->len = 0;
}

#define CHECK(condition, code) do { if (!(condition)) { fprintf(stderr, "ABI check failed at line %d\n", __LINE__); return code; } } while (0)

int main(int argc, char** argv) {
	CHECK(argc == 2, 1);
	void* library = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
	CHECK(library != NULL, 2);
	init_fn init = (init_fn)dlsym(library, "cliproxy_plugin_init");
	CHECK(init != NULL, 3);

	host_api host = {1, NULL, host_call, host_free};
	host_api invalid = host;
	plugin_api plugin = {0};
	invalid.abi_version = 2;
	CHECK(init(&invalid, &plugin) != 0, 4);
	invalid = host; invalid.call = NULL;
	CHECK(init(&invalid, &plugin) != 0, 5);
	invalid = host; invalid.free_buffer = NULL;
	CHECK(init(&invalid, &plugin) != 0, 6);
	CHECK(init(NULL, &plugin) != 0, 7);
	CHECK(init(&host, NULL) != 0, 8);
	CHECK(init(&host, &plugin) == 0, 9);
	CHECK(plugin.abi_version == 1 && plugin.call != NULL && plugin.free_buffer != NULL && plugin.shutdown != NULL, 10);

	cliproxy_buffer response = {0};
	CHECK(plugin.call(NULL, NULL, 0, &response) != 0 && response.ptr != NULL, 11);
	free_plugin_response(&plugin, &response);
	char unknown[] = "unknown";
	CHECK(plugin.call(unknown, NULL, 1, &response) != 0 && response.ptr != NULL, 12);
	free_plugin_response(&plugin, &response);
	uint8_t byte = 0;
	CHECK(plugin.call(unknown, &byte, (size_t)INT_MAX + 1, &response) != 0 && response.ptr != NULL, 13);
	free_plugin_response(&plugin, &response);
	CHECK(plugin.call(unknown, NULL, 0, NULL) != 0, 14);
	CHECK(plugin.call(unknown, NULL, 0, &response) == 0 && response.ptr != NULL, 15);
	free_plugin_response(&plugin, &response);

	char register_method[] = "plugin.register";
	uint8_t empty_config[] = "{}";
	CHECK(plugin.call(register_method, empty_config, 2, &response) == 0, 16);
	free_plugin_response(&plugin, &response);
	CHECK(wait_flag(&oversized_freed), 17);

	atomic_store(&response_mode, 2);
	char reconfigure_method[] = "plugin.reconfigure";
	CHECK(plugin.call(reconfigure_method, empty_config, 2, &response) == 0, 18);
	free_plugin_response(&plugin, &response);
	char scheduler_method[] = "scheduler.pick";
	uint8_t scheduler_request[] = "{\"Provider\":\"claude\",\"Providers\":[\"claude\"],\"Model\":\"claude-fable-5\",\"Candidates\":[{\"ID\":\"auth-a\",\"Provider\":\"claude\"}]}";
	CHECK(plugin.call(scheduler_method, scheduler_request, sizeof(scheduler_request) - 1, &response) == 0, 19);
	free_plugin_response(&plugin, &response);
	CHECK(wait_flag(&malformed_freed), 20);

	plugin.shutdown();
	dlclose(library);
	return 0;
}
`

func TestCSharedABIBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dlopen boundary harness is Unix-only")
	}
	dir := t.TempDir()
	extension := ".so"
	if runtime.GOOS == "darwin" {
		extension = ".dylib"
	}
	library := filepath.Join(dir, pluginName+extension)
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", library, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build C-shared plugin: %v\n%s", err, output)
	}
	runCSharedABIBoundaries(t, library)
}

func runCSharedABIBoundaries(t *testing.T, library string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "abi_boundary.c")
	binary := filepath.Join(dir, "abi_boundary")
	if err := os.WriteFile(source, []byte(abiBoundarySource), 0o600); err != nil {
		t.Fatalf("write ABI boundary harness: %v", err)
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", source, "-o", binary}
	if runtime.GOOS == "linux" {
		args = append(args, "-ldl")
	}
	if output, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile ABI boundary harness: %v\n%s", err, output)
	}
	if output, err := exec.Command(binary, library).CombinedOutput(); err != nil {
		t.Fatalf("run ABI boundary harness: %v\n%s", err, output)
	}
}
