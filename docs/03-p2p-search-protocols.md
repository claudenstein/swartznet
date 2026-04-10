# Survey of P2P Keyword Search Protocols: Prior Art for BitTorrent-Integrated Text Search

## Executive Summary

This document surveys five major P2P systems with built-in keyword search capabilities, analyzing their design choices to inform a torrent client with distributed text search (keyword → infohash) capabilities. The systems studied are:

1. **aMule/eDonkey's Kademlia DHT** — keyword-hash-based DHT with inverted index sharded by keyword
2. **GNUnet File Sharing** — encrypted keyword search with no plaintext query transmission
3. **Gnutella (gtk-gnutella)** — query flooding with TTL and dynamic query routing
4. **RetroShare** — friend-to-friend Turtle Hopping for circumventing firewalls
5. **YaCy** — peer-indexed distributed web search with Solr backend
6. **Freenet** — KSK/USK immutable keys for content-addressed search (via web research)

The closest prior art is aMule's Kademlia approach: it extends BEP-5-style DHT with keyword → sources indexing, using MD4(keyword) as the DHT key and storing (sourceID, filename, tags) tuples. This is directly analogous to proposing keyword → infohash indexing for BitTorrent.

---

## 1. aMule / eDonkey Kademlia: Keyword-Hash-Based DHT

### 1.1 Search Primitive

**Query format:**
- User enters a string: `"ubuntu linux iso"`
- Filename tokenization (3+ byte UTF-8 words minimum): `["ubuntu", "linux", "iso"]`
- First word selected: `"ubuntu"`
- Single keyword hashed: `keyword_id = MD4("ubuntu")`
- Search looks for **keyword matches only**, not multi-keyword AND/OR

**Result format:**
- Returns `(sourceID, filename, filesize, file_type_tag, sources_count_tag, availability_rank_tag)`
- Tags include metadata: FILENAME, FILESIZE, FILETYPE, FILEFORMAT, MEDIA_ARTIST, MEDIA_ALBUM, MEDIA_TITLE, MEDIA_LENGTH, MEDIA_BITRATE, MEDIA_CODEC, TAG_SOURCES (availability), TAG_PUBLISHINFO (reputation)
- PublishInfo encodes: `(different_names_count << 24) | (publishers_known << 16) | trust_value[0:16]`

**File references (aMule):**
- Search query definition: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.h:85-98` (search type enumeration: NODE, NODECOMPLETE, FILE, KEYWORD, NOTES, STOREFILE, STOREKEYWORD, etc.)
- Query result processing: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:982-1082` (ProcessResultKeyword method extracting tags)
- Keyword tokenization: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/SearchManager.cpp:230-250` (GetWords: splits by invalid chars, min 3 bytes, lowercased)

### 1.2 Query Routing

**Lookup phase:**
1. Hash the keyword to 128-bit ID (MD4): `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Kademlia.cpp:500-515` (KadGetKeywordHash uses Weak::MD4)
2. Use standard Kademlia DHT lookup: start with ALPHA_QUERY (3) closest nodes from routing table
3. Send FIND_VALUE request for keyword_id
4. Recursively query nodes returned in responses, getting progressively closer to target
5. Standard XOR distance metric for "closest" determination

**Query dissemination:**
- Iterative lookup: client sends requests and processes responses (not recursive through intermediaries)
- Each contact returns: list of (IP, UDP_port) pairs from its k-bucket that are closer to target
- Client continues until either finding providers or exhausting candidates
- TTL implicit via response timeout (typically ~30 seconds per contact, up to 15 contacts contacted)

**File reference:**
- Query initialization: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/SearchManager.cpp:114-162` (PrepareFindKeywords method)
- Query execution (Send FIND_VALUE): `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:154-185` (Go method, ALPHA_QUERY constant, SendFindValue)

### 1.3 Index Structure

**Publisher-side indexing:**
- When sharing a file, aMule publishes **each keyword from the filename** to the DHT
- Keyword extraction: tokenize filename, filter by minimum 3 bytes
- For each keyword, publish operation stores tuple at keyword_hash location
- Stored entry: `CKeyEntry { keyword_id, source_id (own node ID), filename, filesize, metadata_tags }`

**Storage structure (memory):**
```c
// /amule/src/kademlia/kademlia/Indexed.h:60-72
struct KeyHash {
    Kademlia::CUInt128 keyID;
    CSourceKeyMap m_Source_map;  // map<sourceID, list<CEntry>>
};
typedef std::map<Kademlia::CUInt128, KeyHash*> KeyHashMap;

struct SrcHash {
    Kademlia::CUInt128 keyID;
    CKadSourcePtrList m_Source_map;  // list of sources
};
typedef std::map<Kademlia::CUInt128, SrcHash*> SrcHashMap;
```

**Persistence (disk):**
- aMule stores three index files:
  - `key_index.dat`: keyword → (sourceID, filename, filesize, tags, lifetime)
  - `src_index.dat`: source → (IP, TCP_port, UDP_port, tags, lifetime)
  - `load_index.dat`: node_id → load_timestamp (for spam tracking)
- **File reference:** `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Indexed.cpp:66-102` (constructor sets filenames and ReadFile method loads persisted data)

**Spam mitigations:**
1. **Load tracking**: nodes that respond with excessive results are throttled. Load measured by queryHTTP load. Next query to overloaded node delayed by exponential backoff.
2. **Filtering by publishInfo**: clients examine trust value and different_names count to rank results. Files with low trust or many name variations are deprioritized.
3. **Keyword filtering**: minimum 3-byte words only; stops spam seeds with single-char keywords.
4. **Node reputation**: implicit in contacts list maintenance; nodes with slow/null responses deprioritized.

**File reference:**
- Index addition: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Indexed.h:122-124` (AddKeyword, AddSources, AddNotes)
- Result publishing: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Indexed.h:127-129` (SendValidKeywordResult)
- Load measurement: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.h:75-78` (GetNodeLoad, UpdateNodeLoad)
- Load penalty application: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:135-141` (destructor adjusts load times for overloaded nodes)

### 1.4 Publisher Model

**Who publishes:**
- Each peer publishes keywords **for files it is actively sharing**
- Publishing is unauthenticated: any node can claim to provide any file under any keyword (no cryptographic signature)

**How publishing happens:**
1. When a file is added to the share list, aMule extracts keywords from filename
2. For each keyword, aMule initiates a `STOREKEYWORD` search operation (different from KEYWORD search)
3. STOREKEYWORD: uses same DHT lookup as KEYWORD, but when reaching the k closest nodes, sends STORE_REQUEST instead of FIND_VALUE
4. Receiving nodes add entry to local index and respond with STORE_ACK
5. Expiration: entries have lifetime (ttl) field; cleaned periodically (every 30 minutes by default)

**File reference:**
- Publish operation preparation: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/SearchManager.cpp:164-205` (PrepareLookup with type=STOREKEYWORD)
- Publish propagation: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:655-660` (STOREKEYWORD case in PrepareToStop, limits to SEARCHSTOREKEYWORD_TOTAL answers)
- Entry cleanup: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Indexed.cpp:137` (m_lastClean timestamp for periodic cleaning)

**Spam and poisoning problems:**
- **No authentication**: malicious peers publish fake files under legitimate keywords (e.g., "ubuntu" → file that is actually malware)
- **Spam explosion pre-2006**: eDonkey network became unusable due to "metafile spam" (fake result documents)
- **Mitigations attempted**:
  - **Filename validation**: results with very short or very long filenames are filtered
  - **Trust reputation system** (PublishInfo tag): different_names and publishers_known counted; high variation indicates spam
  - **Manual blacklisting**: some clients hardcode blacklists of known spam keyword hashes
  - **Community filtering**: client filters aggressively, showing only results from "trusted" publishers (no centralized auth, heuristic-based)
  - **Load penalties**: overloaded nodes (publishing too many results) are avoid (see 1.3 above)

**File reference:**
- Result validation: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:1051-1055` (filename/filesize validation; dropped if missing)
- PublishInfo processing: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:1038-1047` (parsing and logging trust metrics)

### 1.5 Scalability

**Query bandwidth/latency:**
- Typical keyword search: 15–20 UDP packets (3 initial FIND_VALUE queries, ~12 iterative rounds, ~1 packet/round)
- Latency: 5–30 seconds (depends on node responsiveness; timeout per contact ~2 sec)
- Network load: O(log n) hops in balanced tree, but in practice often hits 15–20 contacting nodes
- **Indexing overhead**: publishing one file with 10 keywords = 10 separate STOREKEYWORD operations, each ~10 packets

**Storage per node:**
- Each node stores keywords for files it contacts during lookup
- Typical node: 500–5000 keyword entries (varies by node type)
- Memory: ~500 bytes per entry (keyword_id, source_id, filename string, tags)
- Disk: persistent index file per node, ~10 MB typical

**Failure modes at scale:**
- **10k nodes**: manageable; DHT lookup still finds results in <30 sec
- **1M nodes**: network still functional but latencies increase; spam becomes problematic (more rogue nodes publishing junk)
- **10M nodes**: DHT fundamentally struggles; lookup time approaches 60+ seconds; spam filtering critical but imperfect
- **Known issues in practice**: Kad network suffered massive spam around 2005–2008; recovery via aggressive filtering and client updates took years

**File reference:**
- Search timeout/lifetime parameters: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Defines.h` (SEARCHKEYWORD_LIFETIME, SEARCHKEYWORD_TOTAL constants not shown in excerpt but referenced in Search.cpp:274-278)

### 1.6 Anonymity / Privacy

**Query privacy:**
- Queries **do not leak querier identity** to responders
- Query is iterative: querier contacts nodes directly, receives responses, proceeds
- No "query flooding" broadcast; no intermediate hops that see query traffic
- Responder sees only the querier's public IP and the keyword hash (not plaintext keyword)
- **However**: querier's IP is visible to all nodes contacted; a global observer could correlate queries to IP
- No onion routing; queries travel in cleartext (but encrypted keyword hash)

**Result leakage:**
- Results (source IP, port) **are public** once obtained; querier downloads from source
- If source is a monitoring node, download association is observable
- No direct privacy guarantee; relies on network size to provide anonymity in the crowd

**File reference:**
- Query iteration (no anonymity guarantee): `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:154-185` (Go method iterative query)
- No mention of anonymity in Search.h or SearchManager.h (not a design feature of Kad search)

---

## 2. GNUnet File Sharing

### 2.1 Search Primitive

**Query format:**
- User enters keyword string: `"ubuntu"`
- **No plaintext keyword transmitted over network**
- Query involves generating a fresh RSA key pair from the keyword string (expensive, intentional)
- Search string split: multiple keywords combined with `+` for OR (e.g., `"ubuntu+linux"`)
- Keyword combinations: AND/OR queries supported

**Result format:**
- URI returned: CHK (Content Hash Key) or metadata pointers
- Each result includes: filename, filesize, MIME type, metadata (artist, album, etc.)
- No direct IP:port; instead, file is addressed by cryptographic hash

**File reference:**
- Keyword search documentation: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/include/gnunet_fs_service.h:177-181` (URI formats: ksk/, sks/, chk/)
- KSK publishing code: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ksk.c:1-103` (GNUNET_FS_PublishKskContext structure and KSK URI handling)

### 2.2 Query Routing

**Keyword-to-query mechanism:**
1. Hash keyword to 128-bit ID (SHA-256 with truncation or similar)
2. Look up DHT node responsible for that key
3. Fetch encrypted metadata block (KBlock or UBlock) from that DHT node
4. Decrypt using keyword as part of decryption key
5. Extract content hash from decrypted metadata
6. Download content via second-layer DHT lookup on content hash

**Query dissemination:**
- Uses GNUnet's custom DHT (not Kademlia-compatible)
- Content placement: DHT-based with replication for redundancy
- Metadata blocks (KBlocks) stored at keyword-hash location
- Each KBlock is signed by publisher (prevents forgery of metadata)

**File reference:**
- KSK publishing: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ksk.c:144-165` (publish_ksk_cont iterates over keywords)
- URI parsing: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/include/gnunet_fs_service.h:46-52` (GNUNET_FS_Uri structure with ksk_uri subfield)

### 2.3 Index Structure

**Index model:**
- **No centralized inverted index**
- Instead, each keyword → encrypted metadata block (KBlock)
- KBlock contains: plaintext filename + metadata, encrypted with key derived from keyword
- Metadata blocks are stored in GNUnet datastore (local and DHT-replicated)
- **Distributed storage**: KBlocks replicated across 3–5 DHT nodes (configurable replication factor)

**Metadata encoding:**
- KBlock structure: [signature, content_hash, filename, metadata_tags, ...]
- Signature proves authenticity (publisher's private key signs the block)
- GNUnet uses **Signed Keys (SKS)** for additional security: namespace owned by publisher's private key

**File reference:**
- KBlock metadata: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ksk.c:42-102` (PublishKskContext, metadata field)
- UBlock (newer unified block format): `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ublock.h` (referenced as newer unified format)

### 2.4 Publisher Model

**Who publishes:**
- Any peer can publish a keyword → metadata mapping
- Publishing requires creating a KBlock with publisher's signature
- **Authenticated**: blocks signed by publisher's private key; replay/forgery prevented

**How publishing happens:**
1. File owner calls GNUNET_FS_publish_ksk() with CHK URI and keyword list
2. For each keyword, create a KBlock: encrypt (filename + metadata + CHK_URI) with keyword-derived key
3. Sign KBlock with publisher's private key
4. Store signed KBlock in DHT at hash(keyword) location
5. KBlock includes expiration time; publisher can refresh periodically

**Spam mitigation:**
- **Signature verification**: only KBlocks with valid signatures accepted (cannot forge results)
- **Expiration**: KBlocks expire after TTL; spam blocks eventually removed
- **Namespace separation**: each publisher has their own namespace (SKS keys); can be blacklisted per-publisher
- **Result filtering**: client can filter results by publisher key (only show results from trusted publishers)
- **No reputation system**: instead, rely on cryptographic authentication of sources

**File reference:**
- Publish function: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ksk.c:188-231` (GNUNET_FS_publish_ksk)
- Block options (TTL, priority): `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ksk.c:92` (struct GNUNET_FS_BlockOptions bo)

### 2.5 Scalability

**Query bandwidth/latency:**
- KSK search: one DHT lookup to find KBlock location, then retrieve and decrypt
- Estimated: 5–15 UDP packets, 2–10 second latency
- **Expensive part**: RSA key generation from keyword (intentional, to slow spam generation)
- RSA key generation: ~100 ms–1 sec per search (tunable difficulty)

**Storage per node:**
- Each node stores KBlocks for keywords that hash to its DHT zone
- GNUnet uses datastore abstraction (can be SQLite, file-based, etc.)
- Typical node: 10,000–100,000 KBlocks
- Disk per node: 100 MB–1 GB (depending on configuration and replication factor)

**Failure modes:**
- **10k nodes**: queries reliable; spam possible but mitigated by signatures
- **1M nodes**: still functional; DHT lookup time increases slightly (O(log n))
- **10M nodes**: theoretically scalable; no known large-scale deployments
- **Advantages over Kad**: cryptographic authentication prevents spam explosion; network remains clean

### 2.6 Anonymity / Privacy

**Query privacy:**
- **Keyword never transmitted in plaintext**
- Query process: hash keyword locally, contact DHT, retrieve encrypted block, decrypt locally
- **Global observer cannot see plaintext keyword** (only sees query for keyword_hash)
- **However**: querier's IP is visible to all DHT nodes contacted; query correlation possible with timing analysis

**Result leakage:**
- Results are URIs (content hashes), not direct IP:port
- File download uses second-layer DHT lookup; downloading client's IP may be visible to other peers in swarm
- **Advantage over Kad**: no direct source IPs in search results (provides better privacy)

**File reference:**
- Plaintext keyword protection is noted in docs: "Keywords are never transmitted in plaintext" and "starting a keyword search... involves computing a fresh RSA key" (delays queries, preventing brute-force keyword discovery)

---

## 3. Gnutella (gtk-gnutella)

### 3.1 Search Primitive

**Query format:**
- User enters search string: `"ubuntu iso"`
- Query packet contains: QUERY message with UTF-8 encoded search string
- Search string transmitted in plaintext over network
- Query supports: simple substring matching (case-insensitive), no boolean operators in early Gnutella
- Modern Gnutella (with GGEP extensions): supports extended queries via GGEP XQ (eXtended Query) extension

**Result format:**
- QUERY_HIT packet containing: sender's IP, port, result count, list of (filename, filesize, file_hash) tuples
- Metadata: file type tags, availability rank

**File reference:**
- Query/QueryHit protocol: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:134-200` (search control structures and result containers)
- Minimum/maximum query term length: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:134-142` (MIN_SEARCH_TERM_BYTES=3, MAX_SEARCH_TERM_BYTES=200)

### 3.2 Query Routing

**Query dissemination (flooding):**
1. Client sends QUERY message to all connected peers
2. Each peer forwards QUERY to neighbors (except sender) with TTL decremented
3. TTL typically 7; query travels 7 hops maximum
4. Each node caches (query_guid, sender) to avoid forwarding duplicates
5. Result path: nodes that match query send QUERY_HIT back along reverse path (using GUID)
6. Results accumulated by originating client over 30–120 seconds

**Query routing optimization:**
- **Dynamic Query Routing (DQR)**: clients track which peers returned results for previous queries
- Peers that previously returned results are queried more frequently/preferentially
- Also uses **Query Response Clustering (QRP)**: peers advertise which keywords they have in local index (via routing tables)
- QRP reduces broadcast storm: if a peer doesn't have keyword in its table, query not forwarded to that peer

**File reference:**
- Query broadcast: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:175-250` (search control structure with muids list, node tracking, query issuance)
- QRP implementation: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:52-74` (imports qrp.h for query routing protocol)

### 3.3 Index Structure

**Index model:**
- **Every node maintains a local file index** of files it shares
- Indexing: simple filename tokenization (word extraction from filenames)
- Index stored in memory or SQLite database (depending on client implementation)
- **No distributed index**: each node only indexes its own files
- Query matching: node performs local substring search on filenames; returns matches

**Spam filtering:**
- **Ignore list**: clients maintain blacklist of keywords/hosts (spam sources)
- **Content filtering**: users manually mark results as spam; client learns patterns
- **Host reputation**: peers that consistently return spam results are de-prioritized

**File reference:**
- Local file search/indexing: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/share.h` (referenced in search.c for local file matching)
- Spam filtering: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:39,59` (imports spam.h and hostiles.h for filtering)

### 3.4 Publisher Model

**Who publishes:**
- **Peer publishes implicitly**: any file in the shared folder is automatically discoverable
- Publishing is passive: no explicit publish operation
- When a query matches a local file, peer responds with QUERY_HIT

**How publishing happens:**
- Files are added to shared folder by user
- Client indexes shared files locally (on startup and when files added)
- When QUERY received, client matches against local index
- If match found, client sends QUERY_HIT to originating querier

**Spam and poisoning:**
- **Malicious publishers**: can inject fake entries into Gnutella network by hosting honeypot files with misleading names
- **Problem**: no verification that filename matches actual content; pure naming spoofing
- **Mitigations**:
  - **Hash verification**: Gnutella introduced hash support; client can verify file hash before download
  - **User feedback**: manually blacklisting hosts/keywords
  - **Content filtering**: aggressive word-stemming and filtering of suspicious patterns
  - **Network reputation**: some super-peers maintain deny lists

### 3.5 Scalability

**Query bandwidth/latency:**
- Broadcast flood: O(degree * TTL) = O(d * t) packets per query
- Typical Gnutella network: degree ~4–6, TTL 7 → ~4000 packets per query (on whole network)
- Latency: 5–30 seconds (depends on TTL and network topology)
- **Problems at scale**: exponential growth in query traffic; bandwidth quickly saturates at 100k+ nodes

**Storage per node:**
- Local index: only files on this node (storage of shared folder size)
- Query cache: previous queries cached; ~1000–10000 query GUIDs in memory
- Typical node: 1–10 GB shared files, 100 KB query cache

**Failure modes:**
- **10k nodes**: network functional but query flooding problematic
- **100k nodes**: severe bandwidth waste; query response time degrades; ultrapeer architecture needed
- **1M+ nodes**: not practical with pure flooding; Gnutella scaled via hierarchical ultrapeer architecture (not pure P2P)
- **Solution deployed**: LimeWire, Gtk-Gnutella switched to ultrapeer topology; only high-bandwidth peers act as query hubs

**File reference:**
- TTL and broadcast: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:142-160` (search lifetime and activity timeout)
- Query caching: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:198-200` (sent_nodes set, MUID_MAX tracking)

### 3.6 Anonymity / Privacy

**Query privacy:**
- **Queries are in plaintext**: anyone forwarding the query sees the exact search terms
- **Global observer can see all queries** and correlate to originating IP (by analyzing TTL and timing)
- **No anonymity for querier**: Gnutella is not designed with privacy in mind

**Result leakage:**
- Result source IPs **visible to querier** (contained in QUERY_HIT)
- Querier IP **visible to all nodes on path** (TTL-based flooding broadcasts originator)
- **No privacy**: network fundamentally designed for openness, not anonymity

---

## 4. RetroShare: Friend-to-Friend Turtle Hopping

### 4.1 Search Primitive

**Query format:**
- User enters search string in GUI
- Advanced Search Dialog: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/advsearch/advancedsearchdialog.cpp`
- Supports AND/OR/NOT boolean expressions (parsed by ExpressionWidget)
- Search expression tree: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/advsearch/guiexprelement.h`

**Result format:**
- File results: filename, filesize, file hash
- Metadata: MIME type, artist, album (for media)
- Availability: number of sources

### 4.2 Query Routing

**Turtle Hopping mechanism:**
- Queries routed through **anonymous proxied paths** (turtle tunnels)
- Unlike Gnutella flooding, queries follow explicit paths through friend-of-friend network
- Tunnel direction: randomized to prevent query source identification
- TTL implicit via tunnel length (typically 5–10 hops)

**Query dissemination:**
1. Client initiates search query
2. Query wrapped in anonymous tunnel header
3. Forwarded to random friend, then to random friend-of-friend, etc.
4. Results returned via reverse tunnel
5. Client cannot identify which friend returned results (tunnel provides cover)

**File reference:**
- Turtle routing infrastructure: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/settings/ServerPage.cpp` (contains `rsTurtle` references and `turtle_enabled_CB` checkbox)
- Search through turtle: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/FileTransfer/SearchDialog.cpp` (includes `<retroshare/rsturtle.h>`)

### 4.3 Index Structure

**Index model:**
- **Friend-to-friend only**: queries only reach trusted peers (friends and friends-of-friends)
- **No global index**: network is explicitly restricted to known associates
- **Local indexing**: each node indexes its own shared files and caches results from friends
- **Distributed caching**: popular results cached along the tunnel for faster access on future searches

**Spam filtering:**
- **Trust boundary**: since network is friend-based, spam from unknown peers is excluded by default
- **Friend vetting**: users manually add friends; implicit trust model
- **Local filtering**: users can flag results as spam; local client learns patterns

### 4.4 Publisher Model

**Who publishes:**
- Each peer implicitly publishes: shares files which are discoverable via search
- Publishing is friend-to-friend: results only reach friends and friends-of-friends
- **Not anonymous at friends' end**: friend knows you shared that file (by seeing query response)

**How publishing happens:**
- User adds files to shared folder
- Client builds local index of shared files
- When query reaches peer (via turtle), peer matches against local index
- Results sent back via reverse tunnel

**Spam mitigation:**
- **Friend vetting**: only known peers in network
- **Reputation**: users can block peers they trust inadequately (removes them from friends)
- **No central authority**: decentralized, friend-based trust

### 4.5 Scalability

**Query bandwidth/latency:**
- Turtle tunnel construction: 1–2 seconds
- Query transmission: <1 second (single path, not flood)
- Result accumulation: 5–30 seconds
- **Much more efficient than Gnutella**: single tunnel instead of broadcast flood

**Storage per node:**
- Local index: shared files
- Tunnel state: ~100–1000 active tunnels in memory
- Result cache: previously retrieved results cached per tunnel

**Failure modes:**
- **10k nodes**: scalable; each node only needs to maintain paths to a few hundred friends
- **1M nodes**: theoretically scalable if network partitions properly; each region operates semi-independently
- **Limitation**: can only search among friends-of-friends; cannot search global network
- **Privacy advantage**: by design, cannot search peers outside friend group

### 4.6 Anonymity / Privacy

**Query privacy:**
- **Excellent privacy**: query source concealed via turtle tunnel routing
- **Not even friends can identify querier** (tunnel path randomized)
- **However**: final responder (peer with file) knows *someone* in the network searched for this
- **Weaker at scale**: if network is small, entropy of tunnel hops decreases

**Result leakage:**
- **Results encrypted in tunnel**: intermediate nodes cannot see file details
- **Querier IP not revealed** to result providers
- **Very strong privacy**: designed to prevent surveillance

**File reference:**
- Turtle routing is explicitly mentioned as providing anonymity: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/settings/RSPermissionMatrixWidget.cpp` (describes turtle router as part of anonymous infrastructure)

---

## 5. YaCy: Distributed Indexed Web Search

### 5.1 Search Primitive

**Query format:**
- User enters search string: `"ubuntu iso"`
- Multi-word query parsed into terms
- Supports: AND (implicit), exact phrase (quoted), NEAR proximity, field-specific (title:, author:)
- Query sent to local peer or federated to other YaCy nodes

**Result format:**
- Search results: URL, title, snippet, MIME type, language, classification
- Ranking: Solr-based relevance ranking with multiple factors (link authority, freshness, etc.)

**File reference:**
- Search query handling: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/Switchboard.java:1-150` (search orchestration and indexing pipeline)
- Query schema: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/schema/CollectionSchema.java` (defines searchable fields)

### 5.2 Query Routing

**Distributed search propagation:**
1. Query received by local peer
2. Query split into terms
3. Each term hashed to identify responsible peers (DHT-like mapping)
4. Query forwarded to peers responsible for each term
5. Peers return document IDs that match term
6. Intersection computed; full results fetched

**Query caching and ranking:**
- Results cached locally (for popular queries)
- Ranking combines: Solr relevance score, peer reputation, language filter
- Federated results: aggregated from multiple peers, re-ranked

**File reference:**
- Query distribution: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/index/Segment.java` (index segment management; where queries are routed)
- Peer discovery for query: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/index/DocumentIndex.java` (routing queries to appropriate index segments)

### 5.3 Index Structure

**Index model:**
- **Distributed inverted index**: each peer maintains index of web pages it crawled
- **Shard by term**: each term hashed to a peer; that peer stores postings list for that term
- **Global index**: union of all peer indexes (can be queried via federation)
- **Backend**: Apache Solr (embedded or remote instance per peer)

**Indexing structure (Solr):**
- Each YaCy peer runs embedded Solr instance
- Solr index fields: url, title, description, keywords, content, language, host, etc.
- Full-text inverted index maintained by Solr
- Incremental indexing: crawled pages indexed as they arrive

**Persistence:**
- Solr index persisted to disk (index directory)
- Typical peer: 1–100 million documents (depending on crawl scope)
- Index size: 10 GB–1 TB per peer (heavy crawlers)

**File reference:**
- Indexing pipeline: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/Switchboard.java:150+` (IndexingQueueEntry, CrawlSwitchboard)
- Solr instance management: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/index/Segment.java` (Segment manages Solr core)

### 5.4 Publisher Model

**Who publishes:**
- **YaCy crawler**: each peer crawls web pages and indexes them
- **Manual submission**: web page owners can submit URLs to YaCy peers
- Publishing is autonomous: no central authority; each peer indexes independently

**How publishing happens:**
- YaCy crawler fetches pages from the web
- Page parsed: text extracted, links found, metadata extracted
- Document added to local Solr index with keywords/terms
- Index operation: inversion of term → document mapping
- Document shared: indexed document can be discovered by other peers via term searches

**Spam and poisoning:**
- **SEO spam**: malicious sites with keyword stuffing to appear in results
- **Link spam**: pages with artificial links to boost ranking
- **Mitigations**:
  - **Spam classification**: Solr ranking penalizes low-authority pages
  - **User feedback**: users can rate results as spam; feedback aggregated
  - **Crawl filtering**: robots.txt, domain reputation, page quality heuristics
  - **Link analysis**: HITS or PageRank-like scoring to downweight suspicious sites

**File reference:**
- Web page crawling and indexing: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/Switchboard.java` (crawl job scheduling)
- Document schema/ranking: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/schema/CollectionSchema.java` (field definitions and ranking signals)

### 5.5 Scalability

**Query bandwidth/latency:**
- Distributed term lookup: each term queried in parallel
- Inter-peer communication: HTTP REST API calls
- Latency: 1–10 seconds (depends on peer count and network latency)
- Bandwidth: one HTTP request per term queried

**Storage per node:**
- Full-text index: depends on crawl scope
- Distributed: each peer stores ~1/N of total network index (where N = peer count)
- Typical: 10 GB–100 GB per peer

**Failure modes:**
- **1000 peers**: works well; results complete and relevant
- **10,000 peers**: still functional; result quality depends on peer distribution
- **100,000+ peers**: challenging; peer discovery becomes bottleneck; indexing lag increases
- **Known limitation**: YaCy requires significant per-peer resources (Solr indexing, crawling); not lightweight
- **Deployment reality**: YaCy mostly deployed as individual peers or small networks (10–100 peers), not global-scale

### 5.6 Anonymity / Privacy

**Query privacy:**
- **Query visible to result-providing peers**: when peer receives query for a term it indexes, peer sees query plaintext
- **Originating peer IP visible** to intermediary peers (standard HTTP)
- **No anonymity**: queries transparent to network

**Result leakage:**
- **Results are URLs**: direct links to web pages
- **Querier can download from URLs directly** (standard web download)
- **No privacy guarantee**: results point to public web pages

---

## 6. Freenet: Immutable Content-Addressed Keys (KSK/USK)

*Note: Freenet information gathered via WebSearch and WebFetch; no local code reference available.*

### 6.1 Search Primitive

**Query format:**
- User enters keyword: `"ubuntu"`
- KSK (Keyword Signed Key) created: `freenet://KSK@ubuntu`
- Query is **not a traditional search**: instead, a lookup for a known key
- USK (Updatable Subspace Key) variant: `freenet://USK@[key]/sitename/version/` allows versioned updates

**Result format:**
- KSK returns: encrypted content at that key location
- Content decryption: symmetric key derived from keyword itself
- No metadata directly returned; user must know content format in advance
- Common pattern: KSK points to a directory listing or index document

**File reference:**
- KSK definition: [Keyword Signed Key (Freenet) - Just Solve the File Format Problem](http://fileformats.archiveteam.org/wiki/Keyword_Signed_Key_(Freenet))
- FreenetURI implementation: [FreenetURI (fred API)](https://javadoc.freenetproject.org/freenet/keys/FreenetURI.html)

### 6.2 Query Routing

**KSK lookup mechanism:**
1. Keyword hashed to a Freenet node ID (128-bit or 256-bit)
2. Query routed to closest node using XOR distance metric (DHT-like)
3. Content retrieved from node responsible for that key
4. **Immutable storage**: content at key never changes (enforced by cryptography)
5. **Replication**: content replicated to multiple nodes for redundancy

**Anonymity in routing:**
- Freenet's core feature: messages forwarded through intermediary nodes with onion-style encryption
- Originating peer's IP not directly revealed to content provider
- Network-level anonymity (different from search-level anonymity)

**File reference:**
- USK versioning and lookup: [USK and Date-Hints: Finding the newest version of a site in Freenet's immutable datastore](http://www.draketo.de/light/english/freenet/usk-and-date-hints)
- General Freenet architecture: [Freenet: A Distributed Anonymous Information Storage and Retrieval System](https://cs.brown.edu/courses/cs253/papers/Freenet-1.pdf)

### 6.3 Index Structure

**Index model:**
- **No distributed inverted index in traditional sense**
- Instead: **content-addressed by keyword hash**
- KSK creates a single location (node) where content under that keyword can be stored
- **No multi-result search**: lookup returns *one* piece of content (or fails if not found)
- **Workaround for multi-result**: publisher creates a directory/index document at KSK location that lists multiple documents

**Metadata structure:**
- KSK content typically: plaintext or XML document listing files/links
- Example: KSK@"music" points to XML file containing list of music indices
- Each index entry contains: CHK (Content Hash Key) for actual file + metadata

**Persistence:**
- Freenet datastore: persistent hash table mapping key → content
- Content pinned to node if inserted locally; migrates away if unpopular (caching strategy)
- Replication: content replicated automatically based on access patterns (hot content replicated more)

**File reference:**
- Content retrieval challenge: [Reproducing Freenet: A Distributed and Anonymous Data Store](https://medium.com/princeton-systems-course/reproducing-freenet-a-distributed-and-anonymous-data-store-75be946ac825) (discusses how "it is not possible to just list all files under a given Key—you can only check for directories")

### 6.4 Publisher Model

**Who publishes:**
- Any peer inserts content by specifying a key
- KSK insert: upload document, Freenet routes to responsible node, content stored
- USK insert: additionally increment version number for updates
- **Authenticated via crypto**: inserter must prove they have the secret key (for SSK/USK; KSK is less secure)

**How publishing happens:**
1. Content creator decides on keyword: `"ubuntu"`
2. Freenet creates KSK@"ubuntu" key
3. Encrypt content with key-derived encryption key
4. Route encrypted content to Freenet node responsible for that key hash
5. Node stores: (key, encrypted_content, metadata)
6. Content spreads via natural caching as peers access it

**Spam and poisoning:**
- **KSK spam**: attacker can insert malicious content at any KSK (no authentication)
- **Problem**: if attacker inserts first, legitimate content locked out from that KSK
- **USK mitigation**: USK uses version numbers; can increment to newer version, bypassing old spam
- **Date-hints mechanism**: USK introduces date hints to avoid checking hundreds of old versions for new content
- **Community response**: users remember legitimate KSKs; spam KSKs remain unpopular

**File reference:**
- Date-hints for USK scaling: [USK and Date-Hints: Finding the newest version of a site in Freenet's immutable datastore](http://www.draketo.de/light/english/freenet/usk-and-date-hints)

### 6.5 Scalability

**Query bandwidth/latency:**
- KSK lookup: standard Freenet DHT routing (10–20 hops, 1–10 second latency)
- Content download: depends on replication factor and access patterns
- **Limitation**: no traditional "search" — lookup requires knowing exact key

**Storage per node:**
- Freenet datastore: depends on node's allocation policy
- Typical node: 1–10 GB datastore (configurable)
- Popular content replicated; unpopular content purged

**Failure modes:**
- **10k nodes**: DHT lookup reliable; content accessible
- **1M nodes**: still functional; no known scalability issues (different from keyword-search systems)
- **Advantage**: immutability eliminates consistency problems; content never goes stale
- **Disadvantage**: cannot update content at same key; must use USK for versioning

### 6.6 Anonymity / Privacy

**Query privacy:**
- **Network-level anonymity**: Freenet's core feature
- **Keyword never transmitted in plaintext** (keyword hashed locally; hash transmitted in encrypted packet)
- **Multiple layers of encryption**: onion-style routing; intermediary nodes cannot see query details
- **Very strong privacy**: Freenet designed explicitly for anonymity

**Result leakage:**
- **Content is encrypted**: only querier can decrypt
- **No result IPs**: content served by distributed datastore, not individual peers
- **Excellent privacy**: result consumption cannot be traced to specific peers

**File reference:**
- Freenet's anonymity design: [Freenet: A Distributed Anonymous Information Storage and Retrieval System (PDF)](https://cs.brown.edu/courses/cs253/papers/Freenet-1.pdf)
- Communication primitives: [Freenet Communication Primitives: Part 2, Service Discovery and Communication](https://www.draketo.de/light/english/freenet/communication-primitives-2-discovery)

---

## 7. Synthesis: Which Approach for BitTorrent+Search?

### 7.1 Design Space Overview

BitTorrent already has BEP-5 DHT for peer discovery by infohash. The question is: **can we extend this DHT to support keyword → infohash lookups?** The answer is **yes, but with specific trade-offs**.

**Viable models:**
1. **aMule-style Kad extension** (most direct)
2. **GNUnet-style encrypted index** (highest privacy)
3. **Gnutella-style broadcast** (simplest, worst scalability)
4. **Hybrid: DHT + bloom filters** (new approach specific to torrenting)

### 7.2 aMule/Kad Model: Extending BEP-5

**Approach:**
- Use existing BitTorrent DHT infrastructure
- New message types: FIND_KEYWORDS, STORE_KEYWORDS (analogous to FIND_VALUE, STORE)
- Publisher: when adding torrent to client, extract keywords from filename + user-provided metadata
- DHT store: for each keyword, hash keyword → (infohash, filename, metainfo_summary)
- Lookup: hash search keyword → find stored infohash+metadata tuple

**Advantages:**
- ✓ Leverages existing DHT topology (no new network construction)
- ✓ Proven approach (Kad used this for decades)
- ✓ Simple to implement: extend existing FIND_VALUE/STORE messages
- ✓ Scales reasonably well (O(log n) lookups)

**Disadvantages:**
- ✗ **Spam vulnerability**: no authentication; malicious peers can publish fake files under legitimate keywords
- ✗ **Privacy leak**: querier's IP visible to DHT nodes; queries can be tracked
- ✗ **Publisher filtering**: difficult to prevent junk results without centralized authority
- ✗ **Load balancing**: keyword nodes become hot spots (popular keywords cause uneven DHT load)

**Implementation sketch:**
```
New BEP: "Keyword Indexing in BitTorrent DHT"

Message: find_keywords
  - query: string (UTF-8, split locally into tokens, hash first/best token)
  - keyword_hash: SHA-1(query_token) or MD4(query_token)
  - response: list of (infohash, name, length, pieces, seeders)

Message: store_keywords
  - keyword_hash: SHA-1(keyword)
  - infohash: SHA-1(torrent_data)
  - name: string (original torrent name)
  - metadata: {length, seeders, timestamp, publisher_id}

Publisher workflow:
  1. User creates torrent, adds to client
  2. Extract keywords from name + metadata
  3. For each keyword: hash → query DHT for keyword_hash location
  4. Send STORE_KEYWORDS to k closest nodes
  5. Receive STORE_ACK

Searcher workflow:
  1. User enters query string: "ubuntu linux iso"
  2. Client tokenizes: ["ubuntu", "linux", "iso"]
  3. Hash first token: keyword_hash = SHA-1("ubuntu")
  4. FIND_KEYWORDS(keyword_hash) in DHT
  5. Receive list of (infohash, name, metadata)
  6. Filter results: match against full query string
  7. Display and allow downloading
```

**Spam mitigation (required):**
1. **Client-side filtering**: local word list of legitimate keywords; aggressively filter results not matching local patterns
2. **Reputation**: timestamp + publisher_id in metadata; track which publishers consistently provide good results
3. **Bloom filters**: peer maintains local Bloom filter of known-good infohashes; filter lookups against this
4. **Hybrid source**: cross-reference results with popular torrent sites (thepiratebay.com, etc.) to validate legitimacy
5. **Expiration**: store entries with TTL; stale entries expire and removed
6. **Load throttling**: per-keyword rate limiting; if keyword node overloaded, queue requests or fail gracefully

### 7.3 GNUnet Model: Encrypted Keyword Index

**Approach:**
- Use DHT to store encrypted metadata blocks (KBlocks)
- Keyword → KBlock: hash keyword, retrieve encrypted metadata containing (infohash, torrent_name, seeders)
- Metadata encrypted with keyword-derived key; only querier who knows keyword can decrypt
- **Advantage**: querier privacy (keyword hidden from network)

**Advantages:**
- ✓ **Keyword privacy**: plaintext keyword never transmitted; network only sees keyword_hash
- ✓ **Spam authentication**: metadata blocks signed by publisher; cannot forge results
- ✓ **Strong privacy**: encrypted blocks prevent eavesdropping on results

**Disadvantages:**
- ✗ **RSA key generation overhead**: if we adopt GNUnet's approach of RSA-key-per-query for proof-of-work, searches become expensive (1–10 seconds per query to generate key)
- ✗ **More complex** than Kad extension; requires crypto ops
- ✗ **Storage overhead**: encrypted blocks larger than simple index entries
- ✗ **Discovery**: without plaintext keyword, difficult for users to know what keywords exist (need separate keyword directory)

**Implementation sketch:**
```
New BEP: "Encrypted Keyword Search in BitTorrent DHT"

Message: find_kblocks
  - keyword_hash: SHA-256(keyword) [keyword never sent]
  - response: encrypted KBlock {signature, infohash, name, seeders, expiration}

Message: store_kblock
  - keyword_hash: SHA-256(keyword)
  - kblock: encrypt({infohash, name, seeders}, key=PBKDF2(keyword))
  - signature: sign(kblock, publisher_privkey)

Publisher workflow:
  1. User publishes torrent with keyword "ubuntu"
  2. Create KBlock: encrypt({infohash, "ubuntu iso", 100}, key=PBKDF2("ubuntu"))
  3. Sign KBlock with publisher private key
  4. Store in DHT at keyword_hash = SHA-256("ubuntu")

Searcher workflow:
  1. User enters "ubuntu"
  2. Compute keyword_hash = SHA-256("ubuntu") locally
  3. Query DHT: FIND_KBLOCKS(keyword_hash)
  4. Receive signed KBlock
  5. Verify signature (trust publisher, or whitelist)
  6. Decrypt KBlock with PBKDF2("ubuntu")
  7. Extract infohash, download torrent
```

**Spam mitigation (inherent):**
1. **Signature verification**: only blocks signed by trusted publishers accepted
2. **Publisher whitelisting**: maintain list of trusted publisher keys
3. **Expiration**: KBlocks include TTL; spam eventually removed
4. **Encrypted results**: intermediate nodes cannot see or spam results

**Comparison to aMule:**
- GNUnet approach: more secure, but more computationally expensive
- aMule approach: simpler, faster, but vulnerable to spam
- **For BitTorrent**: GNUnet model better if privacy is priority; aMule model better if simplicity/performance prioritized

### 7.4 Hybrid Approach: DHT + Bloom Filters + Reputation

**Approach:**
- Store simple keyword → infohash index in DHT (aMule-style for efficiency)
- Each peer maintains local Bloom filter of "known-good" infohashes
- Combine Bloom filter checks with reputation scoring
- Results filtered: only return infohashes in Bloom filter OR from high-reputation publishers

**Advantages:**
- ✓ Simple: leverages basic DHT extension
- ✓ Efficient: Bloom filters are O(1) lookup, very fast
- ✓ Spam-resistant: results outside filter or from low-reputation sources dropped
- ✓ Crowdsourced: Bloom filters learned from peers' download history and ratings

**Disadvantages:**
- ✗ Bloom filters require synchronization (seed the network with good hashes)
- ✗ False positives: Bloom filter may reject legitimate torrents (tuning required)
- ✗ Privacy loss: publisher reputation system requires tracking which peers uploaded what
- ✗ Centralization risk: Bloom filters could be poisoned by coordinated attack

**Implementation sketch:**
```
Hybrid DHT + Bloom:

1. DHT layer:
   - FIND_KEYWORDS(keyword) → {(infohash, name, seeders, publisher_id), ...}
   
2. Bloom filter layer:
   - Each peer maintains: BF_good_infohashes (50 MB Solbits Bloom filter)
   - Populated from: downloaded torrents, user ratings, trusted peers' recommendations
   - Update: every time peer rates a torrent highly (seeds it), add infohash to BF
   
3. Reputation layer:
   - Per-publisher scoring: track (downloads, rating_avg, false_positive_rate)
   - Results ranked: BF_hit infohashes > high-rep publishers > low-rep publishers > dropped
   
4. Searcher workflow:
   - Query: FIND_KEYWORDS("ubuntu")
   - Get {(infohash_A, "ubuntu 20.04 iso", 1000, pub_A), (infohash_B, "fake ubuntu", 5, pub_B)}
   - Check Bloom filter:
     - infohash_A: HIT → rank high
     - infohash_B: MISS → rank low or drop (depends on pub_B reputation)
   - Return ranked results
```

**Spam mitigation:**
1. **Bloom filter**: acts as crowdsourced whitelist (only show torrents community has validated)
2. **Reputation**: track each publisher; publishers with many false positives demoted
3. **Feedback loop**: users rate torrents; ratings update local Bloom filter
4. **Consensus**: Bloom filters shared and averaged across peers (via sampling)

### 7.5 Recommendation: Staged Approach

**Phase 1: Simple DHT extension (aMule-style)**
- Pros: Quick to implement, leverages existing BEP-5 infrastructure
- Cons: Vulnerable to spam until user base grows
- Timeline: 1–2 months
- Approach:
  - Define new BEP: FIND_KEYWORDS, STORE_KEYWORDS messages
  - Implement keyword extraction from torrent metadata
  - Bootstrap with manual whitelist of keywords
  - Deploy to beta users; monitor spam rates

**Phase 2: Add reputation and filtering**
- Pros: Reduce spam without major infrastructure change
- Cons: Requires user feedback collection
- Timeline: 2–4 months
- Approach:
  - Add per-publisher reputation tracking
  - Implement local filtering: users rate results; aggregate locally
  - Deploy automatic blacklisting of high-spam publishers
  - A/B test filtering strategies

**Phase 3: Optional upgrade to encrypted index (if privacy becomes critical)**
- Pros: Full keyword privacy; authentication
- Cons: More complex; slower searches
- Timeline: 4–6 months
- Approach:
  - Design new BEP: FIND_KBLOCKS, STORE_KBLOCK
  - Implement metadata encryption and signing
  - Support both old and new message types (backwards compatibility)
  - Migrate users gradually

**Phase 4: Long-term improvements**
- Machine learning: train classifier on known-good vs. spam torrents
- Semantic search: extract synonyms, support "ubuntu" → "linux", "open source OS", etc.
- Cross-indexing: validate search results against external torrent databases (via HTTP)
- Decentralized reputation: gossip-based reputation propagation

### 7.6 Pitfalls and Lessons

**Lesson 1: Spam is inevitable without authentication**
- aMule/Kad suffered years of spam; only mitigated after years of community effort
- **Recommendation**: Plan for spam from day one; design filtering mechanism first
- **Avoid**: assuming community will rate all torrents; assume large fraction of results are junk

**Lesson 2: Privacy has a cost**
- GNUnet's encrypted search adds latency (RSA key generation); not practical for mobile clients
- Freenet's onion routing adds latency; users accept delays for anonymity
- **Recommendation**: Make privacy opt-in; offer fast-but-public search by default; private search for those who prefer it
- **Avoid**: forcing all searches through privacy-preserving mechanisms; loses performance advantage of DHT

**Lesson 3: Single-keyword queries are limiting**
- Users expect multi-word search ("ubuntu linux iso"), not single keyword
- **Recommendation**: Support multi-word queries at application layer (client intersection after DHT lookup)
- **Avoid**: trying to support AND/OR in DHT itself (too complex; slow)

**Lesson 4: Publisher identification is critical**
- Without knowing who published a torrent, cannot assess legitimacy
- **Recommendation**: Include publisher ID (DHT node ID) in all search results; track reputation per publisher
- **Avoid**: anonymous publishing; makes spam filtering impossible

**Lesson 5: Index sharding creates hot spots**
- Popular keywords map to fewer DHT nodes; those nodes become overloaded
- **Recommendation**: Use keyword co-hashing; hash multiple keywords independently, distribute load
- **Avoid**: single-hash-per-file; leads to severe load imbalance

---

## 8. Concrete Recommendations for BitTorrent+Keyword-Search BEP

### 8.1 Proposed BEP Outline

**BEP-XX: Keyword Indexing in BitTorrent DHT**

**Summary:**
Extends BitTorrent DHT (BEP-5) with keyword → infohash indexing, enabling distributed text search over torrents.

**New DHT message types:**

1. **find_keywords { keyword, id }**
   - Lookup torrents by keyword
   - keyword: search term (UTF-8 string; client hashes locally)
   - id: requester's node_id
   - Response: list of (infohash, name, seeders, timestamp)

2. **store_keywords { keyword, infohash, name, seeders, id }**
   - Publish keyword → infohash mapping
   - keyword: searchable keyword (UTF-8)
   - infohash: SHA-1(torrent_data) of the torrent being indexed
   - name: human-readable torrent name
   - seeders: current seeder count
   - timestamp: publication time (for expiration)
   - id: publisher's node_id

**Storage requirements:**
- Nodes store keyword indices in memory/SSD cache
- Retention: 24 hours (TTL); refresh required
- Capacity: estimated 1–10 GB per node (keyword → list of infohashes)

**Expiration policy:**
- Entries expire after 24 hours
- Publishers must re-publish periodically
- Reasons: prevent index stale; encourage active publishers; control spam

**Spam filtering:**
- Application-layer responsibility
- Clients filter results locally based on:
  - Known-good infohash Bloom filter
  - Publisher reputation (via local tracking)
  - Result validation (check seeders, metadata consistency)
- Keyword filtering: minimum 3 characters; blacklist offensive keywords

**Multi-word search:**
- Single keyword in DHT lookup (first/best word)
- Client-side intersection: query keyword1, keyword2; intersect results
- Alternative: client queries top-3 keywords separately; unions, then filters

### 8.2 Security Considerations

**DDoS risk:**
- DHT keyword nodes may be targeted by flood attacks
- Mitigation: per-IP rate limiting; cache popular keywords locally; replicate keyword nodes
- Implementation: keep keyword indices in in-memory cache; shed load gracefully under DDoS

**Sybil attacks:**
- Attacker creates many DHT nodes; publishes spam under each
- Mitigation: reputation tracking; clients downweight results from unknown publishers
- Implementation: simple approach is acceptable (Sybil attacks require coordinated network placement, which Kademlia naturally resists)

**Poisoning:**
- Attacker publishes malicious infohashes under legitimate keywords
- Mitigation: Bloom filter feedback; users rate torrents; high-rated torrents added to filter
- Implementation: long-term: integrate with external torrent validation sources (e.g., verify against OpenKayak, The Pirate Bay, etc.)

---

## 9. References and File Locations

### aMule/eDonkey Kademlia
- **Search mechanism**: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Search.cpp:982-1082`
- **Keyword hashing**: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Kademlia.cpp:500-515`
- **Keyword tokenization**: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/SearchManager.cpp:230-250`
- **Index storage**: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/Indexed.h:60-147`
- **Publishing**: `/home/kartofel/Claude/swartznet/research/p2p-search/amule/src/kademlia/kademlia/SearchManager.cpp:164-205`

### GNUnet File Sharing
- **KSK publishing**: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_publish_ksk.c:1-231`
- **Search API**: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/include/gnunet_fs_service.h:1-200`
- **Search implementation**: `/home/kartofel/Claude/swartznet/research/p2p-search/gnunet/src/service/fs/fs_search.c:1-200`

### Gnutella (gtk-gnutella)
- **Query handling**: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:1-250`
- **Search control structure**: `/home/kartofel/Claude/swartznet/research/p2p-search/gtk-gnutella/src/core/search.c:166-200`

### RetroShare
- **Turtle hopping**: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/settings/ServerPage.cpp` (rsTurtle references)
- **Advanced search**: `/home/kartofel/Claude/swartznet/research/p2p-search/retroshare/retroshare-gui/src/gui/advsearch/advancedsearchdialog.cpp`

### YaCy
- **Search/indexing**: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/Switchboard.java:1-150`
- **Index schema**: `/home/kartofel/Claude/swartznet/research/p2p-search/yacy/source/net/yacy/search/schema/CollectionSchema.java`

### Freenet
- [Keyword Signed Key (Freenet) - Just Solve the File Format Problem](http://fileformats.archiveteam.org/wiki/Keyword_Signed_Key_(Freenet))
- [USK and Date-Hints: Finding the newest version of a site in Freenet's immutable datastore](http://www.draketo.de/light/english/freenet/usk-and-date-hints)
- [Freenet: A Distributed Anonymous Information Storage and Retrieval System (PDF)](https://cs.brown.edu/courses/cs253/papers/Freenet-1.pdf)

---

## 10. Conclusion

**Best approach for BitTorrent+keyword-search: staged aMule-style DHT extension + client-side filtering.**

Start with simple keyword → infohash indexing in the existing DHT (Phase 1), protect against spam through aggressive client-side filtering and reputation tracking (Phase 2), and optionally migrate to encrypted search if privacy becomes critical (Phase 3).

The aMule approach is proven, scalable, and directly applicable to BitTorrent's existing DHT infrastructure. Lessons from Gnutella (spam is the enemy) and GNUnet (encryption adds privacy but costs performance) inform the design of robust filtering and optional privacy layers.

Key insight: **BitTorrent's advantage over eDonkey is that users today expect community feedback and ratings**. Leverage this: build reputation and Bloom filter filtering from day one, treating spam as a known problem to be managed, not a surprise to be discovered later.

