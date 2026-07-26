package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

static const cliproxy_host_api* stored_host;

static int host_api_valid(const cliproxy_host_api* host, uint32_t abi_version) {
	return host != NULL && host->abi_version == abi_version && host->call != NULL && host->free_buffer != NULL;
}

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type cgoHostClient struct{}

type authListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type hostLogRequest struct {
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func installHost(raw unsafe.Pointer) bool {
	host := (*C.cliproxy_host_api)(raw)
	if C.host_api_valid(host, C.uint32_t(pluginabi.ABIVersion)) == 0 {
		return false
	}
	C.store_host_api(host)
	return true
}

func (cgoHostClient) listAuth() ([]pluginapi.HostAuthFileEntry, error) {
	result, err := callHost(pluginabi.MethodHostAuthList, map[string]any{})
	if err != nil {
		return nil, err
	}
	var response authListResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("decode host auth list result: %w", err)
	}
	return response.Files, nil
}

func (cgoHostClient) getAuth(authIndex string) (json.RawMessage, error) {
	result, err := callHost(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return nil, err
	}
	var response pluginapi.HostAuthGetResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("decode host auth get result: %w", err)
	}
	return response.JSON, nil
}

func (cgoHostClient) log(level, message string, fields map[string]any) {
	_, _ = callHost(pluginabi.MethodHostLog, hostLogRequest{Level: level, Message: message, Fields: fields})
}

func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback payload %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		allocated := C.CBytes(rawPayload)
		if allocated == nil {
			return nil, fmt.Errorf("allocate host callback payload %s", method)
		}
		defer C.free(allocated)
		requestPtr = (*C.uint8_t)(allocated)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	if response.ptr != nil {
		defer C.free_host_buffer(response.ptr, response.len)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code %d", method, int(callCode))
	}
	if response.ptr == nil || response.len == 0 {
		return nil, fmt.Errorf("host callback %s returned no response", method)
	}
	if uint64(response.len) > uint64(^uint32(0)>>1) {
		return nil, fmt.Errorf("host callback %s response is too large", method)
	}
	rawResponse := C.GoBytes(response.ptr, C.int(response.len))
	var env envelope
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmt.Errorf("decode host callback envelope %s: %w", method, err)
	}
	if !env.OK {
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	return env.Result, nil
}
