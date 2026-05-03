// Tiny single-purpose helpers that speak the y-websocket binary
// envelope. The relay forwards bytes between clients without
// decoding; these helpers are used only on the slow paths:
//   1. peekMessageType — read the first byte (0=sync, 1=awareness,
//      2=session-end-server-only).
//   2. peekSyncSubType — read the sub-type after a sync (0=step1,
//      1=step2-update, 2=step2-reply).
//   3. encodeSyncStep1Empty — build the message the relay sends to
//      ask the leader for a full state snapshot.
//   4. decodeYTextBody — extract the plain string from a `body`
//      Y.Text inside an encodeStateAsUpdate(doc) byte stream.
//      Used only on finalize.
//
// We intentionally do NOT depend on a Go Yjs implementation. The
// helpers are minimal and well-tested.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// peekMessageType returns the first byte of a y-websocket frame and
// whether the buffer was non-empty. It does not advance any cursor.
func peekMessageType(b []byte) (byte, bool) {
	if len(b) == 0 {
		return 0, false
	}
	return b[0], true
}

// peekSyncSubType returns the sub-type for a y-websocket sync
// message (frames whose first byte is 0). Returns (0, false) for
// non-sync frames or short buffers.
func peekSyncSubType(b []byte) (byte, bool) {
	if len(b) < 2 || b[0] != 0 {
		return 0, false
	}
	// Yjs varint — for sub-types 0/1/2 the value fits in one byte
	// (no high-bit continuation), so a one-byte read is correct.
	if b[1] >= 0x80 {
		return 0, false
	}
	return b[1], true
}

// encodeSyncStep1Empty builds the y-protocols sync-step-1 message
// with an empty state vector, which Yjs interprets as "send me your
// full state."
//
// Wire format:
//
//	byte 0: messageType = 0 (sync)
//	byte 1: syncSubType varint = 0 (step-1)
//	byte 2: stateVector length varint = 0 (empty)
func encodeSyncStep1Empty() []byte {
	return []byte{0, 0, 0}
}

// Yjs info-byte flag layout (verified against
// node_modules/yjs/src/utils/encoding.js readClientsStructRefs):
//
//	bits 0..4 (mask 0x1F): struct/content type code
//	  0  = GC (no content; payload is a varint length)
//	  1  = ContentDeleted (payload: varint length)
//	  2  = ContentJSON
//	  3  = ContentBinary
//	  4  = ContentString (payload: varint length + UTF-8 bytes)
//	  5  = ContentEmbed
//	  6  = ContentFormat
//	  7  = ContentType (nested Yjs type)
//	  8  = ContentAny
//	  9  = ContentDoc
//	  10 = Skip (payload: varint length)
//	bit 5 (0x20, BIT6): hasParentSub (parent sub-key string follows
//	                    parent reference, only when cantCopyParentInfo)
//	bit 6 (0x40, BIT7): hasRightOrigin → readRightID (2 varints)
//	bit 7 (0x80, BIT8): hasOrigin → readLeftID (2 varints)
//
// "cantCopyParentInfo" means BIT7 and BIT8 are both clear: the item
// has neither a left nor right origin and is therefore directly
// attached to a top-level type. In that case the encoder writes a
// readParentInfo varint (1 = string parent name follows, 0 = the
// parent is identified by a leftID pair).
const (
	yjsBit6 = 0x20
	yjsBit7 = 0x40
	yjsBit8 = 0x80
)

// decodeYTextBody walks a Y.encodeStateAsUpdate(doc) byte stream
// and extracts the plain text content of the named top-level Y.Text.
// Used only on finalize.
//
// IMPORTANT: this decoder is correctness-best-effort. Real Yjs
// merge semantics (CRDT ordering by ID, deletions, formatting,
// concurrent inserts) require the full Yjs algorithm. For the
// war-room composer the leader's full Y.Doc is encoded into a
// single linear update on finalize and the common case is one or a
// few sequential ContentString inserts on a single Y.Text named
// "body" — the decoder targets that case.
//
// Plan B (documented in spec §"Out-of-Scope"): if rendered text
// ever diverges from the decoded string, the finalize handler
// instead asks the leader for the plain text via a new message-type
// 3 RPC frame and bypasses Go-side decoding entirely.
//
// Behaviour contract:
//   - never panics on malformed input;
//   - returns ("", err) when the leading numClients varint cannot be
//     read at all (truly malformed);
//   - returns the best-effort partial string accumulated so far on
//     mid-stream errors;
//   - returns "" with no error for an empty doc ([]byte{0,0}).
func decodeYTextBody(update []byte, fieldName string) (string, error) {
	if len(update) == 0 {
		return "", errors.New("decodeYTextBody: empty update")
	}
	r := bytes.NewReader(update)

	numClients, err := readVarUint(r)
	if err != nil {
		return "", fmt.Errorf("decodeYTextBody: read numClients: %w", err)
	}
	if numClients == 0 {
		return "", nil
	}

	var buf bytes.Buffer
	// Track whether the last successfully-parsed item belonged to
	// fieldName. Subsequent items with hasOrigin (left origin) likely
	// chain off it (sequential append/insert into the same Y.Text).
	// This is the heuristic that makes simple sequential inserts
	// work; it does not generalise to deletions or concurrent edits.
	lastBelongedToField := false

	for c := uint64(0); c < numClients; c++ {
		numStructs, err := readVarUint(r)
		if err != nil {
			return buf.String(), nil
		}
		// clientID
		if _, err := readVarUint(r); err != nil {
			return buf.String(), nil
		}
		// clock
		if _, err := readVarUint(r); err != nil {
			return buf.String(), nil
		}
		for s := uint64(0); s < numStructs; s++ {
			info, err := r.ReadByte()
			if err != nil {
				return buf.String(), nil
			}
			structType := info & 0x1F
			hasOrigin := info&yjsBit8 != 0
			hasRightOrigin := info&yjsBit7 != 0
			hasParentSub := info&yjsBit6 != 0
			cantCopyParentInfo := !hasOrigin && !hasRightOrigin

			// GC and Skip carry only a varint length.
			if structType == 0 || structType == 10 {
				if _, err := readVarUint(r); err != nil {
					return buf.String(), nil
				}
				lastBelongedToField = false
				continue
			}

			if hasOrigin {
				// readLeftID = clientID varint + clock varint
				if _, err := readVarUint(r); err != nil {
					return buf.String(), nil
				}
				if _, err := readVarUint(r); err != nil {
					return buf.String(), nil
				}
			}
			if hasRightOrigin {
				if _, err := readVarUint(r); err != nil {
					return buf.String(), nil
				}
				if _, err := readVarUint(r); err != nil {
					return buf.String(), nil
				}
			}

			parentMatched := false
			if cantCopyParentInfo {
				// readParentInfo: 1 → readString (top-level name);
				// 0 → readLeftID (parent identified by item ID pair).
				parentInfo, err := readVarUint(r)
				if err != nil {
					return buf.String(), nil
				}
				if parentInfo == 1 {
					name, err := readVarString(r)
					if err != nil {
						return buf.String(), nil
					}
					if name == fieldName {
						parentMatched = true
					}
				} else {
					// Parent is an item ID — skip 2 varints.
					if _, err := readVarUint(r); err != nil {
						return buf.String(), nil
					}
					if _, err := readVarUint(r); err != nil {
						return buf.String(), nil
					}
				}
				if hasParentSub {
					if _, err := readVarString(r); err != nil {
						return buf.String(), nil
					}
				}
			}

			// Read content payload according to struct type.
			switch structType {
			case 1: // ContentDeleted: varint length
				if _, err := readVarUint(r); err != nil {
					return buf.String(), nil
				}
			case 4: // ContentString: varint length + UTF-8 bytes
				content, err := readVarString(r)
				if err != nil {
					return buf.String(), nil
				}
				belongs := false
				if cantCopyParentInfo {
					belongs = parentMatched
				} else if hasOrigin {
					// Heuristic: an item with a left origin chains off
					// a previous item; if that previous item belonged
					// to fieldName, this one likely does too.
					belongs = lastBelongedToField
				}
				if belongs {
					buf.WriteString(content)
				}
				lastBelongedToField = belongs
			default:
				// Other content types (JSON / Binary / Embed / Format
				// / Type / Any / Doc) need type-specific decoding we
				// don't implement. Bail out with what we have.
				return buf.String(), nil
			}
		}
	}
	// Trailing delete-set is intentionally ignored — we already used
	// the live content above.
	return buf.String(), nil
}

// readVarUint reads a Yjs (lib0) unsigned varint from r.
func readVarUint(r *bytes.Reader) (uint64, error) {
	var result uint64
	var shift uint
	for i := 0; i < 10; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint64(b&0x7F) << shift
		if b < 0x80 {
			return result, nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("varint overflow")
}

// readVarString reads a Yjs varint length followed by that many
// UTF-8 bytes and returns them as a Go string.
func readVarString(r *bytes.Reader) (string, error) {
	n, err := readVarUint(r)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	// Guard against absurd lengths from malformed input. 1 MiB is
	// far above any reasonable field-name or single-insert payload.
	if n > 1<<20 {
		return "", fmt.Errorf("readVarString: implausible length %d", n)
	}
	if uint64(r.Len()) < n {
		return "", io.ErrUnexpectedEOF
	}
	out := make([]byte, n)
	if _, err := io.ReadFull(r, out); err != nil {
		return "", err
	}
	return string(out), nil
}
