// Package stdlib declares dependencies for gig interpreter.
//
// Packages are categorized as SAFE (no host/external interaction) or UNSAFE
// (filesystem, network, process, or system access). Only SAFE packages are
// enabled by default. Uncomment UNSAFE packages as needed.
//
// After editing, regenerate with: gig gen ./stdlib
package stdlib

import (
	// ============================================
	// SAFE: Pure computation, no host interaction
	// ============================================

	// --- String & byte manipulation ---
	_ "bufio"
	_ "bytes"
	_ "strconv"
	_ "strings"
	_ "unicode"
	_ "unicode/utf16"
	_ "unicode/utf8"

	// --- Formatted I/O ---
	_ "fmt"

	// --- Math ---
	_ "math"
	_ "math/big"
	_ "math/bits"
	_ "math/cmplx"
	_ "math/rand/v2"

	// --- Collections & sorting ---
	_ "cmp"
	_ "container/heap"
	_ "container/list"
	_ "container/ring"
	_ "maps"
	_ "slices"
	_ "sort"

	// --- Encoding & serialization ---
	_ "encoding/ascii85"
	_ "encoding/asn1"
	_ "encoding/base32"
	_ "encoding/base64"
	_ "encoding/binary"
	_ "encoding/csv"
	_ "encoding/hex"
	_ "encoding/json"
	_ "encoding/pem"

	// --- Error handling ---
	_ "errors"

	// --- Regular expressions ---
	_ "regexp"
	_ "regexp/syntax"

	// --- HTML escaping ---
	_ "html"

	// --- Time ---
	_ "time"

	// --- Hashing (non-crypto, pure computation) ---
	_ "hash/adler32"
	_ "hash/crc32"
	_ "hash/crc64"
	_ "hash/fnv"
	_ "hash/maphash"

	// --- Cryptographic hashing (pure computation, no I/O) ---
	_ "crypto/hmac"
	_ "crypto/md5"  // #nosec G501 -- Registered for Go compatibility, not recommended for security use.
	_ "crypto/sha1" // #nosec G505 -- Registered for Go compatibility, not recommended for security use.
	_ "crypto/sha256"
	_ "crypto/sha512"
	_ "crypto/subtle"

	// --- Symmetric ciphers (pure computation, no I/O) ---
	_ "crypto/aes"
	_ "crypto/cipher"
	_ "crypto/des" // #nosec G502 -- Registered for Go compatibility, not recommended for security use.
	_ "crypto/elliptic"
	_ "crypto/rc4" // #nosec G503 -- Registered for Go compatibility, not recommended for security use.

	// --- Path manipulation (pure string ops, no filesystem) ---
	_ "path"

	// --- Concurrency (needed because gig supports `go` keyword) ---
	_ "context"
	_ "sync"

	// --- I/O interfaces (needed for encoding/json.NewDecoder etc.) ---
	_ "io"

	// --- Text processing ---
	_ "text/scanner"
	_ "text/tabwriter"

	// --- URL / IP parsing (pure string ops, no network) ---
	_ "net/netip"
	_ "net/url"

	// --- Archive (pure computation on byte streams) ---
	_ "archive/tar"
	_ "archive/zip"

	// --- Compression ---
	_ "compress/bzip2"
	_ "compress/flate"
	_ "compress/gzip"
	_ "compress/lzw"
	_ "compress/zlib"

	// --- MIME ---
	_ "mime"
	_ "mime/multipart"
	_ "mime/quotedprintable"

	// --- Image (color only, pure math) ---
	_ "image/color"

	// --- Misc pure computation ---
	_ "index/suffixarray"
	// ============================================
	// UNSAFE: Host/external interaction — disabled
	// ============================================
	// --- Filesystem ---
	// _ "os"             // file I/O, env vars, process exit
	// _ "os/exec"        // execute external commands
	// _ "os/signal"      // signal handling
	// _ "os/user"        // user account lookups
	// _ "path/filepath"  // OS-specific path ops (needs os)
	// _ "io/fs"          // filesystem abstraction
	// --- Network ---
	// _ "net"            // TCP/UDP sockets, DNS resolution
	// _ "net/http"       // HTTP client/server
	// _ "net/mail"       // mail parsing
	// _ "net/smtp"       // SMTP client
	// _ "net/rpc"        // RPC client/server
	// --- Logging (writes to os.Stderr) ---
	// _ "log"
	// _ "log/slog"
	// --- Templates (can call arbitrary methods on objects) ---
	// _ "html/template"
	// _ "text/template"
	// --- Concurrency (low-level, sync.Mutex is sufficient) ---
	// _ "sync/atomic"
	// --- System/runtime ---
	// _ "syscall"
	// _ "runtime"
	// _ "embed"
	// --- Crypto (asymmetric — depends on crypto/rand) ---
	// _ "crypto/rand"    // reads from /dev/urandom
	// _ "crypto/ecdsa"
	// _ "crypto/ed25519"
	// _ "crypto/rsa"
	// _ "crypto/tls"     // network + filesystem
	// _ "crypto/x509"    // filesystem (cert store)
	// --- Database ---
	// _ "database/sql"
	// ============================================
	// Custom third-party libraries (add yours below)
	// ============================================
	// _ "github.com/spf13/cast"
	// _ "github.com/tidwall/gjson"
)
