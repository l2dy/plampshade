```
// Package lampshade provides a transport between Lantern clients and proxies
// that provides obfuscated encryption as well as multiplexing. The protocol
// attempts to be indistinguishable in content and timing from a random stream
// of bytes, and mostly follows the OBFS4 threat model -
// https://github.com/Yawning/obfs4/blob/master/doc/obfs4-spec.txt#L35
//
// Unlike OBFS4, lampshade does not employ padding and instead relies on the
// fact that TCP is a streaming protocol that doesn't preserve message
// boundaries. In practice, for small messages, TCP does often preserve message
// boundaries, so to thwart fingerprinting Lampshade coalesces consecutive small
// writes into single larger messages when possible. This also improves
// performance by reducing fixed overhead for things like TCP headers.
//
// Definitions:
//
//   physical connection - an underlying (e.g. TCP) connection
//
//   stream              - a virtual connection multiplexed over a physical
//                         connection
//
//   session             - unit for managing multiplexed streams, corresponds
//                         1 to 1 with a physical connection
//
// Protocol:
//
//   Seession initiation
//
//      client --> client init --> server
//
//   Write
//
//      client --> frame --> server
//      client --> frame --> server
//      client --> frame --> server
//      ... continue up to transmit window
//      client <--  ack  <-- server
//      client --> frame --> server
//      client <--  ack  <-- server
//      client <--  ack  <-- server
//      client --> frame --> server
//      client --> frame --> server
//      ... etc ...
//
//   Read (parallel to write)
//
//      client <-- frame <-- server
//      client <-- frame <-- server
//      client <-- frame <-- server
//      ... continue up to transmit window
//      client -->  ack  --> server
//      client <-- frame <-- server
//      client -->  ack  --> server
//      client -->  ack  --> server
//      client <-- frame <-- server
//      client <-- frame <-- server
//      ... etc ...
//
// General Protocol Features
//
//   - protocol attempts to be indistinguishable in content and timing from a
//     random stream of bytes, following the OBFS4 threat model -
//     https://github.com/Yawning/obfs4/blob/master/doc/obfs4-spec.txt#L35
//
//  - all numeric fields are unsigned integers in BigEndian format
//
// Client Init Message
//
//   - 256 bytes
//   - combined with first data message to vary size
//
//   To initialize a session, the client sends the following, encrypted using
//   RSA OAEP using the server's PK
//
//     +---------+-----+--------+---------+---------+
//     | Version | Win | Secret | Send IV | Recv IV |
//     +---------+-----+--------+---------+---------+
//     |    1    |  1  |   16   |   16    |   16    |
//     +---------+-----+--------+---------+---------+
//
//       Version - the version of the protocol (currently 1)
//
//       Win - transmit window size
//
//       Secret - 128 bits of secret for AES128
//
//       Send IV - 128 bits of initialization vector for CTR mode on AES128 for
//                 messages from client -> server
//
//       Recv IV - 128 bits of initialization vector for CTR mode on AES128 for
//                 messages from server -> client
//
// Framing:
//
//   - all frames are encrypted with AES128 in CTR mode, using the secret and
//     IV sent in the Init Session message
//
//   +--------------+-----------+----------+--------+
//   | Message Type | Stream ID | Data Len |  Data  |
//   +--------------+-----------+----------+--------+
//   |       1      |     3     |     2    | <=8192 |
//   +--------------+-----------+----------+--------+
//
//   Message Type - 1 byte, indicates the message type.
//
//  		0 = data
//      1 = ack
//	  	2 = rst (close connection)
//
//   Stream ID - unique identifier for stream. (last field for ack and rst)
//
//   Data Len - length of data (only used for message type "data")
//
//   Data - data (only used for message type "data")
//
```
