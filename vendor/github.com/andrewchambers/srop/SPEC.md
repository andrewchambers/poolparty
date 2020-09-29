
# SROP Specification

A client connects to an SROP server over a reliabled ordered stream such as a TCP socket
and is immediately able to send serialized messages addressed to the root server object which
always has the known object id '0'. 

Each message sent has a destination object id, a request id, a message type id, and a message payload.

A server may process and respond to messages out of order, but messages always arrive in the order they were sent.

The server root object is free to allocate new objects and associate them with any object id in response to messages
or otherwise. Once new object ids are assigned, messages may be sent to these objects by the client.

Request ids are effectively never reused as they are incremented until overflow.

## Wire format

All integers are sent as little endian.

### Request message

RequestID: uint64, ObjectID: uint64, MessageType: uint64, MessageLen: uint64, MessageData: bytes[MessageLen]

### Response message

RequestID: uint64, ResponseType: uint64, ResponseLen: uint64, ResponseData: bytes[ResponseLen]

## Known message types

### Type 0xcf3a50d623ee637d (Clunk)

An empty message sent to an object request it to 'clunk' itself.
Clunking means to remove itself from the connection and do any cleanup
necessary.

### Type 0xd782cf4b395eca05 (Object ref)

It is common to request the creation of a new object, this message can
be used in a reply to signal to the client the id of the newly created object.

This message is frequently used when a new object is created as the result of a message.

This is a [BARE](https://baremessages.org) encoded message with the following schema:

```
type ObjectRef {
  id uint
}
```

### Type 0xd4924862b91c639d (Ok)

An empty message acknowledging something, usually sent as a response to a message such as a clunk request.

### Type 0xab0547366de885bc (Object does not exist)

This message is usually sent when a message has been sent to an object that does not exist, that 
is the SROP server will reply with this message as if the 

The message body is empty.

### Type 0xd47d4e94917934b2 (Unexpected message)

This message is usually sent when an object recieves a message it did not expect to receive.

The message body is empty.

## Creating application specific messages

Generate a random 64 bit integer, if the type is internal only to your application, this should be sufficient. If this
type is shared across many applications, make a pull request to this repository describing your type.

## Design notes

- Version negotiation is not needed, messages are self describing.
- Mixed encoding schemes is no problem, each type id also describes the encoding.
- Once a connection is assigned an object id, that is the [capability](https://en.wikipedia.org/wiki/Capability-based_security)
  to send it messages. This is the basis of capability based security in SROP servers.
- The protocol uses at least 24 bytes per message, so is not particularly efficient for many small messages. This sacrifice
  is made for simplicity and flexibility.