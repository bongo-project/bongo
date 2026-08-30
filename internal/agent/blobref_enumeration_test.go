package agent

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// This file is the guard against a third round of the blob-GC reference bug.
//
// Twice now a field that pins a blob was absent from collectReferencedBlobs:
// the spool's QueueEnvelope.blob_hash, and OutboundMessage.attachments (task
// 14.4), the latter of which silently relayed outbound mail with its
// attachments stripped. Both were found only after the fact. The failure mode
// is structural rather than careless -- someone adds a blob-bearing field to a
// protobuf, and nothing anywhere connects that edit to a garbage collector in
// another package that must learn about it.
//
// TestEveryBlobBearingProtoFieldIsAccountedFor closes that loop. It walks the
// bongo.v1 descriptors at runtime, finds every field that can name a blob, and
// requires each one to have an explicit entry below. A new field fails the test
// until someone states what it is, which forces the decision rather than
// letting it be missed.
//
// The entries are not merely a checklist. Each field marked as part of the
// reference set carries a populate func, and TestReferenceSetCoversEveryPinning
// Field runs them for real: it writes a record bearing a unique hash and
// asserts collectReferencedBlobs returns it. So "covered" means demonstrated,
// not asserted -- deleting the attachments loop from collectReferencedBlobs
// fails this file even though the allowlist is untouched.
//
// Known limit, stated rather than hidden: classifying a field is a judgement
// no test can make. A genuinely pinning field marked transient with a
// plausible-sounding why passes everything here, and its blobs go unprotected.
// The guard cannot prevent that; what it does is refuse to let the field pass
// unnoticed, and require a written claim about what pins the blob instead --
// which is a claim a reviewer can check. Treat a new transient entry as the
// part of a diff that deserves the most attention.

// blobFieldKind says whether a blob-bearing proto field pins a blob against
// garbage collection.
type blobFieldKind int

const (
	// pinning: the field lives in a record persisted in Pebble or the spool,
	// so a blob it names must be in the reference set.
	pinning blobFieldKind = iota
	// transient: the field only ever appears in an in-flight gRPC message. It
	// names a blob that some persisted record is separately responsible for
	// pinning, so scanning it would be meaningless -- there is nothing to
	// scan, these values are never at rest.
	transient
)

// blobBearingField records one proto field that can name a blob, and either
// how the reference set covers it or why it needs no coverage.
type blobBearingField struct {
	kind blobFieldKind
	// why explains the classification. For transient fields this is the whole
	// justification, so it must say what pins the blob instead.
	why string
	// populate writes a record exercising this field, with the given hash, and
	// is required for pinning fields. It returns an error rather than taking
	// *testing.T so the table stays a plain data declaration.
	populate func(ps *store.PebbleStore, spool *pipeline.SpoolStore, hash string) error
}

// knownBlobBearingFields is keyed by "<Message>.<field>" in the bongo.v1
// package. Every field the descriptor walk finds must appear here, and every
// entry here must still exist in the descriptors -- a stale entry fails too,
// so a removed field cannot leave a misleading claim of coverage behind.
var knownBlobBearingFields = map[string]blobBearingField{
	"MailMeta.raw_message": {
		kind: pinning,
		why:  "the message body, at /user/{id}/mail/{guid}",
		populate: func(ps *store.PebbleStore, _ *pipeline.SpoolStore, hash string) error {
			return putMailMeta(ps, "enum-raw", &bongov1.MailMeta{
				Guid:       "enum-raw",
				RawMessage: &bongov1.BlobRef{Hash: hash},
			})
		},
	},
	"MailMeta.attachments": {
		kind: pinning,
		why:  "de-MIMEd attachment bodies, at /user/{id}/mail/{guid}",
		populate: func(ps *store.PebbleStore, _ *pipeline.SpoolStore, hash string) error {
			return putMailMeta(ps, "enum-att", &bongov1.MailMeta{
				Guid:        "enum-att",
				Attachments: []*bongov1.BlobRef{{Hash: hash}},
			})
		},
	},
	"OutboundMessage.blob_hash": {
		kind: pinning,
		why:  "the queued message, or its skeleton when layout is set, under /queue/pending/ and /queue/bounced/",
		populate: func(ps *store.PebbleStore, _ *pipeline.SpoolStore, hash string) error {
			return putQueued(ps, "/queue/pending/00000000000000000900-enum-blob", &bongov1.OutboundMessage{
				Id: "enum-blob", BlobHash: hash,
			})
		},
	},
	"OutboundMessage.attachments": {
		kind: pinning,
		why:  "attachment-dedup bodies the queue worker reassembles before relaying; the gap fixed in task 14.4",
		populate: func(ps *store.PebbleStore, _ *pipeline.SpoolStore, hash string) error {
			return putQueued(ps, "/queue/bounced/enum-att", &bongov1.OutboundMessage{
				Id: "enum-qatt", Attachments: []*bongov1.BlobRef{{Hash: hash}},
			})
		},
	},
	"QueueEnvelope.blob_hash": {
		kind: pinning,
		why:  "the in-flight message body on disk; the one reference source with no Pebble key at all",
		populate: func(_ *store.PebbleStore, spool *pipeline.SpoolStore, hash string) error {
			if spool == nil {
				return fmt.Errorf("spool store required")
			}
			return spool.WriteEnvelope("quarantine", &bongov1.QueueEnvelope{
				Id: "enum-env", BlobHash: hash,
			})
		},
	},
	"DepositResponse.blob_hash": {
		kind: transient,
		why: "gRPC reply telling a remote agent where its deposit landed. The blob is pinned by " +
			"the QueueEnvelope the deposit creates, not by this reply, which is never persisted.",
	},
	"FetchBlobRequest.blob_hash": {
		kind: transient,
		why: "gRPC request naming a blob to stream back. Read-only and never at rest, so there is " +
			"nothing to scan; whatever record referred the caller here is what pins it.",
	},
}

func putMailMeta(ps *store.PebbleStore, guid string, meta *bongov1.MailMeta) error {
	b, err := proto.Marshal(meta)
	if err != nil {
		return err
	}
	return ps.Set([]byte("/user/enum-user/mail/"+guid), b)
}

func putQueued(ps *store.PebbleStore, key string, msg *bongov1.OutboundMessage) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return ps.Set([]byte(key), b)
}

// discoverBlobBearingFields walks the bongo.v1 descriptors and returns every
// field that can name a blob, keyed as "<Message>.<field>".
//
// Two shapes count. A field of message type bongo.v1.BlobRef is the explicit
// one. A string field named blob_hash is the implicit one, and it is the shape
// that has actually caused both incidents -- QueueEnvelope and OutboundMessage
// both name a blob with a bare string rather than a BlobRef, so a walk looking
// only for the BlobRef type would have missed exactly the fields that went
// wrong.
func discoverBlobBearingFields() map[string]bool {
	const blobRefName = "bongo.v1.BlobRef"
	found := map[string]bool{}

	var walk func(md protoreflect.MessageDescriptor)
	walk = func(md protoreflect.MessageDescriptor) {
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			isBlobRef := f.Kind() == protoreflect.MessageKind &&
				string(f.Message().FullName()) == blobRefName
			isHashString := f.Kind() == protoreflect.StringKind &&
				strings.HasSuffix(string(f.Name()), "blob_hash")
			if isBlobRef || isHashString {
				found[fmt.Sprintf("%s.%s", md.Name(), f.Name())] = true
			}
		}
		nested := md.Messages()
		for i := 0; i < nested.Len(); i++ {
			walk(nested.Get(i))
		}
	}

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != "bongo.v1" {
			return true
		}
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			walk(msgs.Get(i))
		}
		return true
	})
	return found
}

// TestEveryBlobBearingProtoFieldIsAccountedFor fails when a proto field that
// can name a blob has no entry in knownBlobBearingFields, and when an entry
// names a field that no longer exists.
//
// The first direction is the one that matters: it is the tripwire for adding a
// blob-bearing field without teaching the GC about it. The second stops the
// table decaying into stale claims of coverage.
func TestEveryBlobBearingProtoFieldIsAccountedFor(t *testing.T) {
	found := discoverBlobBearingFields()

	if len(found) == 0 {
		t.Fatal("descriptor walk found no blob-bearing fields at all, which cannot be right; " +
			"the bongo.v1 protos are probably not linked into this test binary, so this " +
			"guard would silently pass forever")
	}

	var undeclared []string
	for name := range found {
		if _, ok := knownBlobBearingFields[name]; !ok {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf("proto field(s) that can name a blob have no entry in knownBlobBearingFields: %v\n\n"+
			"A blob named by a field the GC does not scan is unreferenced, and blob GC deletes "+
			"unreferenced blobs. Blobs are content-addressed and shared, so losing one loses that "+
			"content for every user holding it, with no undo.\n\n"+
			"Add an entry saying which it is:\n"+
			"  - pinning:   the field is in a record persisted to Pebble or the spool. Teach "+
			"collectReferencedBlobs to scan it, and give the entry a populate func so this file "+
			"proves the coverage.\n"+
			"  - transient: the field only ever appears in an in-flight gRPC message. Say in why "+
			"which persisted record pins the blob instead.", undeclared)
	}

	var stale []string
	for name := range knownBlobBearingFields {
		if !found[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("knownBlobBearingFields names field(s) that no longer exist in the protos: %v\n"+
			"Remove the entries, and check whether collectReferencedBlobs still scans for them.", stale)
	}
}

// TestReferenceSetCoversEveryPinningField is what makes the table above mean
// something. For each pinning field it writes a record carrying a hash unique
// to that field, then asserts collectReferencedBlobs returns every one.
//
// Because the hashes are per-field, a failure names the exact field whose
// references are being dropped rather than reporting a count mismatch. And
// because the records are written through the real key layouts, this fails if
// collectReferencedBlobs stops scanning a prefix, stops reading a field, or
// starts rejecting a key shape it should accept.
func TestReferenceSetCoversEveryPinningField(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	// hash per field, so a dropped reference names the field responsible.
	wantHash := map[string]string{}
	names := make([]string, 0, len(knownBlobBearingFields))
	for name := range knownBlobBearingFields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		f := knownBlobBearingFields[name]
		if f.kind != pinning {
			continue
		}
		if f.populate == nil {
			t.Errorf("%s is marked pinning but has no populate func, so nothing proves the "+
				"reference set actually covers it", name)
			continue
		}
		hash := "enumhash-" + strings.ReplaceAll(name, ".", "-")
		if err := f.populate(ps, spool, hash); err != nil {
			t.Fatalf("populating %s: %v", name, err)
		}
		wantHash[name] = hash
	}

	if len(wantHash) == 0 {
		t.Fatal("no pinning fields were populated; this test would pass vacuously")
	}

	ref, err := collectReferencedBlobs(ps, spool)
	if err != nil {
		t.Fatalf("collectReferencedBlobs: %v", err)
	}

	for _, name := range names {
		hash, ok := wantHash[name]
		if !ok {
			continue
		}
		if !ref[hash] {
			t.Errorf("%s: a blob named only by this field was not in the reference set, so blob GC "+
				"would reclaim it while the record still points at it (%s)",
				name, knownBlobBearingFields[name].why)
		}
	}
}

// TestTransientBlobFieldsExplainWhatPinsTheBlob keeps the transient escape
// hatch honest. Marking a field transient removes it from GC consideration
// entirely, so the entry has to say what pins the blob instead -- otherwise
// the classification is just a way to silence the tripwire.
func TestTransientBlobFieldsExplainWhatPinsTheBlob(t *testing.T) {
	for name, f := range knownBlobBearingFields {
		if f.kind != transient {
			continue
		}
		if len(f.why) < 40 {
			t.Errorf("%s is marked transient with a why of %q; state which persisted record "+
				"pins the blob instead, since nothing else will", name, f.why)
		}
		if f.populate != nil {
			t.Errorf("%s is marked transient but has a populate func; transient fields are not "+
				"part of the reference set, so populating one would assert coverage that does "+
				"not exist", name)
		}
	}
}
