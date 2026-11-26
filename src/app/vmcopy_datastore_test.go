package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
)

// A datastore /folder server that honours Range, ignores it, or (like
// vCenter's proxy) answers 200 when the range covers the whole file.
func TestDatastoreFileReadsCorrectlyWhetherOrNotRangeIsHonoured(t *testing.T) {
	data := make([]byte, 3<<20+12345)
	rand.Read(data)
	descriptor := []byte("# Disk DescriptorFile\nversion=1\nCID=fffffffe\nparentCID=ffffffff\ncreateType=\"vmfs\"\n\nRW " + itoa(int64(len(data))/512) + " VMFS \"disk-flat.vmdk\"\n\nddb.adapterType = \"lsilogic\"\n")
	data = data[:len(data)/512*512]
	for _, mode := range []string{"honour", "ignore", "whole"} {
		t.Run(mode, func(t *testing.T) {
			var gets, full int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					t.Errorf("method %s sent", r.Method)
				}
				content := data
				if strings.HasSuffix(r.URL.Path, "/disk.vmdk") {
					content = descriptor
				}
				if r.Method == http.MethodGet {
					atomic.AddInt32(&gets, 1)
				}
				rangeHeader := r.Header.Get("Range")
				whole := rangeHeader == "bytes=0-"+itoa(int64(len(content))-1)
				if mode == "ignore" || (mode == "whole" && whole) || rangeHeader == "" {
					if r.Method == http.MethodGet {
						atomic.AddInt32(&full, 1)
					}
					w.Header().Set("Content-Length", itoa(int64(len(content))))
					w.WriteHeader(http.StatusOK)
					if r.Method == http.MethodGet {
						w.Write(content)
					}
					return
				}
				http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(content))
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL + "/sdk")
			client := &vim25.Client{Client: soap.NewClient(u, true)}
			ctx := context.Background()
			source, err := openDatastoreVMDK(ctx, client, "DC0", "[ds1] vm/disk.vmdk")
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if source.Size() != int64(len(data)) {
				t.Fatalf("size %d, want %d", source.Size(), len(data))
			}
			// Forward reads, a skip, then a read behind the last one.
			for _, at := range []int64{0, 1 << 20, 2<<20 + 512, 4096, int64(len(data)) - 1000} {
				buffer := make([]byte, 1000)
				n, err := source.ReadAt(buffer, at)
				if err != nil && err != io.EOF {
					t.Fatalf("read at %d: %v", at, err)
				}
				if !bytes.Equal(buffer[:n], data[at:at+int64(n)]) || n != 1000 {
					t.Fatalf("read at %d returned the wrong %d bytes", at, n)
				}
			}
			if mode == "honour" && full != 0 {
				t.Fatalf("%d whole-file reads from a server that honours Range", full)
			}
		})
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
