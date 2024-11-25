# ZeroEventHub spec


## Abstract

This spec details an HTTP API that:

1) Allows a service to publish events to an arbitrary number of subscribers
   without going through an event broker (such as Event Hub)

2) Can work as a supplement to Event Hub. It uses the same basic
   model and services can offer it *in addition* to publishing on Event Hub.

3) Even for consumers opting to consume through Event Hub instead, the
   API is a convenient alternative for backfilling old data.

A parallel is ZeroMQ. In 2007 ZeroMQ launched as a framework and
series of patterns for doing message passing "better" without broker;
it turned out that for message passing, many applications become more
robust and scalable simply by removing the broker (that was in the first
place introduced to make things robust).

This spec attempts the same for Event Hub-style event channels.

## Goal of proposal

Async events through Azure Event Hub between services has some benefits:

* Events defined in standard contracts in contract-catalog, culture of centralizing
  persistable events as source of truth

* Events can be transmitted without producer depending on consumer being alive
  and receiving, and possibly needing to implement retries (as callbacks require)

There are also some problems:

* With Event Hub you don't have consistency; you don't know that if you did
  something with a service the effect will be on the event feed the moment
  you get a response.

* Event Hub adds a latency that is not directly controllable and
  may in certain "tight" situations add operational risk.

* Event Hub adds a extra piece of infra that must be up, which may be
  a liability on a critical path. Abusing a famous quote: "The only
  problem that cannot be solved by adding another moving piece is too
  many moving pieces."

The goal of this spec is to keep most of the first advantages, while eliminating
these two problems.

The scope is primarily a backend producer which sits on top of a
database and first commits new state to the database, before
publishing events of what has happened and already been committed to
Event Hub. We are aware that Event Hub can have other usecases and
this spec may be less relevant for such usecases.

## Q & A

**What if the consumer goes down?**

The publisher doesn't know about consumers in this spec. The pattern
allow consumers to resume at any point, just like for Event Hub.

**What if the publisher goes down?**

In this situation Event Hub would seem to give higher uptime, *but, consider:*

Scenario 1: The consumer is OK with some latency and consumes
irregularly/lazily/in a batch.  In such a scenario, it may often be OK
that events are delayed a bit more if the producer is down.

Scenario 2: The consumer needs minimal latency. In this situation the consumer
hogs the feed and reads events as soon as they are published.
Now... when the producer goes down, new events will also stop! Event Hub
actually doesn't help in this scenario. A very few events may have been caused
by the producer but not read by the consumer, but it is also the case that a few
events may have been caused by the producer but not yet been written to Event Hub
when the publisher went down. So this is the same as Event Hub.

**What about load on the publisher?**

In situations where the number of consumers is very high and all are reading a lot
of data this is a legitimate advantage of Event Hub; Event Hub helps distribute
the load of reading events.

Note also that Azure SQL databases have read-only replicas that can take
this load; in the case of Azure SQL Hyperscale you can scale those to any number
and even dedicate them to certain consumers.

**Will this make it harder for those using Event Hub?**

The API is compatible with Event Hub in such a way that a standard
service that doesn't need to touch the event payloads can handle generic
forwarding from the ZeroEventHub API to Event Hub. Therefore it should
be easy enough to provide Event Hub in addition when that is wanted.

**Why not an existing API spec?**

If you know about one we'd love to use it instead of NIH.

The closest thing we found is Atom / RSS. But these are really made
for other usecases and in particular don't have features for sharding
or consumer groups. We would need to extend them anyway -- at that
point it is better to make something a lot simpler than those
standards.


## Specification: HTTP API for consistent publishing of events

We assume that the Publisher is a service that has a database with the
source of truth and consistent state of the data; and that changes to
this database is streamed as events to Event Hub.

The publisher can then additionally make the events available in a polling
interface, which is made available to select consumers.
The endpoint the backend makes the API available on is not defined
(and each backend can choose to expose several event feeds on different routes).


### Partitions

Partitions are provided so that event processing can more easily be
parallelized both at the Producer and the Consumer. In this example
the publisher has documented that 4 partitions is available -- the
client cannot change this, but has to document its assumption in the
`n` parameter. Also the client chooses to process 2 of these
parititions in a single request -- presumably there is another thread
processing the remaining 2 partitions in parallel. This method of
maintaining independent cursor allows the consumer flexibility in
advancing all cursors in parallel in one chain of calls, or to orchestrate
multiple chains of calls on different subset of partitions, and to split &
merge partitions at will (up to the limit supported by the producer).

**WARNING**: If comparing with the same events published on Event Hub,
while the partition keys should be the same, the mapping of partition
keys to partitions will be different. Events will be "re-partitioned"
when published to Event Hub vs. the Event API.


### Authorization & use of consumer identity

Standard service-to-service MSI, out of scope for this spec.

As part of the authorization the publisher will usually figure out a
name/identity of the consumer. The publisher may use this to log access,
and can also use this as a hint to guide the implementation (e.g.,
whether the load should hit a secondary cold storage or the freshest
hot storage, or what named SQL Hyperscale read-replica should serve
the request).

### Number of partitions

The publisher documents the current native number of partitions
(physical partitions). An API for querying this may be added in the
future.

The consumer specifies its preferred number of partitions to work with
(i.e., how many workers it prefers to work with) as the `n` parameter.
It is strongly encouraged to make the number of partitions a power of
two and servers may opt to only support this.

*MVP implementation:* If the consumer number of partitions mismatches
the server number of physical partitions, there is an error. Consumers
uses the features to ask for several partitions in one stream if they
want fewer partitions.

*Proper implementation:* This is typically implemented the first time
we need it. The idea is that the publisher can present a virtual view
of the number of partitions for the consumer. The case where `n` is
smaller is trivial; the server can merge several physical partitions
into fewer logical ones. If `n` is larger than the real partition
number then the server one simulate empty partitions without events,
or use a more complex setup to "re-partition" on the fly.

### Example request:

```
GET https://myservice/my-kind-of-entity/feed/v1?cursor0=1000240213123&cursor1=1231231231242&pagesizehint=1000&n=4&headers=ce_tracestate,ce_id
```


### Parameters

* **n**: Number of partitions the client assumes the server to have,
  in total.  If there is mismatch, the server is free to either
  emulate the behaviour or return 400 Bad Request.

* **cursorN**: Pass in one cursor for each partition you wish to
  consume; where `N` is a number in the range `0...n-1`.
  Each `cursor` is an opaque string that should be passed
  back as-is, but is limited to ASCII printable characters. Two special
  cursors are used; `_first` means to start from the beginning of time,
  and `_last` starts at an arbitrary point "around now".

* **pagesizehint**: Return at most this many events from the request
  in total. The number is only a *hint*, consumers should handle
  receiving more events than this, and receiving less events does not
  mean that more events are not immediately available. The number is
  the total number in the response; some publishers may simply divide
  it by number of cursorN provided and use that as a max per
  cursor. The parameter is optional and the producer should choose a
  sensible default for the dataset.

* **headers**: In event transports (such as Event Hub) the headers are
  primarily of use to middlewares. With zeroeventhub the consumption is more
  direct, and therefore headers of events are not returned by default,
  and the header parameter is used to request which headers one wants.
  The special value `_all` can be used to request all headers. The parameter
  is optional and its absence means that no headers will be returned.

See the example above for more detailed description of the interaction of
`n` and `cursorN`.

Consumers are encouraged to pass contiguous ranges of cursors and the
same number of cursors from every thread. I.e., pass
`cursor2=...&cursor3=...`, DO NOT pass `cursor2=...&cursor4=...` (not
contiguous) and DO NOT pass `cursor1=...&cursor2=...` (does not spread
evenly across threads). This recommentation in this spec makes the
pattern predictable in the case that producers can optimize for it.

### Response

The response is served as new-line-delimited JSON [NDJSON Wikipedia](https://en.wikipedia.org/wiki/JSON_streaming#Newline-Delimited_JSON).
Each line contains *either* an event, *or* a checkpoint.

The rationale for an NDJSON format is that the same specification will work
with very long-running HTTP requests, with websockets, etc., without changes
to the streaming format itself.

#### Events

An event has the form `{"partition": ..., "headers": {...}, "data": { ... }}`,
here is an example displayed with whitespace for clarity
(newlines must not be present within each event on the wire):

```
{
  "partition": 0,
  "headers": {
     "header1": "value1",
     "header2": "value2",
     ...
  },
  "data": {
     "my": ["nonescaped", "json"]
   } // JSON included structurally
}
```

If `header` is empty -- as is the case when not requesting headers in the request --
it can be non-present in the struct

#### Checkpoint

A checkpoint has the form `{"partition": ..., "cursor": ...}`. The client can save this
cursor value in order to start the stream at the same point.

Between checkpoints, events are unsorted and may arrive in a different order if the
same/similar request is done again.

Checkpoints are also allowed to come back differently if the same/similar request is done
again. This is because the cursor may really be a composite cursor on several internal
partitions in the service, and a checkpoint be emitted for an advance of any individual
internal partition. The requirement for checkpoints is simply that if a client
persisted all events *before* a checkpoint, and then passes the checkpoint cursor
in on the next call, then it will be able to properly follow the stream of the events.

### Recommendations

* The consumer is advised to persist the cursor state in the same
  database transaction that stores the event data consumer-side; this
  pattern guarantees exactly-once delivery of the stream. The publisher
  SHOULD document whether the publisher may re-publish events, or whether
  the consumer can rely on exactly-once delivery.

* The publisher should document if it is **consistent** -- that is,
  that it can guarantee that if you first do something with the service
  (e.g. POST something), then consume its event feed after the REST
  calls returns, that what you just did will *always* be found immediately
  in the event feed.


## Possible future extension: Publisher-side cursors ("consumer group")

This feature can be added in particular to make it possible to add
standard forwarders who do not have the capability to store the
cursors consumer-side.  For consumers that have access to a database
it is discouraged to store cursors publisher-side, and in particular
it is always preferable to transactionally store the cursors together
with the written result of processing the events.  But, for the cases
where we need to support publisher-side storage we have this
feature, using two extra arguments:

* **cursorset=<name>**: This makes the Producer persist the cursor you
  provide as *input* for each partition under the name passed in a
  simple key/value store.  You can then use them using the `resume`
  flag. ASCII max length 50.

* **resume=0,1,2,3,...**: If this flag is passed, do not pass
  `cursors`. Instead the `cursors` that was provided in the previous
  call as *input* will be loaded and used. The partitions to resume
  are given as argument. The Producer simply stores the last input
  cursor for `(consumername, partition)` in a key value store, and the
  Consumer has to ensure that the set of partitions to be resumed
  makes sense and that there are no problems with splitting & merging
  workers vs.  this mechanism. Typical single-threaded consumers have
  to pass `0,1,...,31`.

Typically there is not a need to inform the publisher that an event page
has been processed, because that will coincide with reading the next page
(and this is why the input is saved as a checkpoint). And with any at-least-once
processing you need to handle re-processing anyway! But, if there is a real
need to simply store a state without reading more events you can always
pass `store_cursor=1&pagesizehint=0`...

## Possible future extension: Long-polling

An extra argument:

* **wait=NNN** If there are no new events, wait for this many seconds
  before returning.

This allows clients to open an HTTP request and get back a response
the moment an event happens.

## Future possibilities

* The newline-delimited JSON works for other formats more typical for streaming
  data, such as websockets and grpc.

* It would be possible to define a consistency model
  similar to Cosmos' (strong, bounded staleness, etc.), and also one that
  interacted with other APIs the publisher provides, so that one had
  a way to express guarantees that operations done on other REST APIs
  was visible in this event API. So far the uses in practice seems fairly limited for this.

* One could allow subscribing to subsets of the event feed. Such extensions
  may be standardized or just bolted on by each backend in a manner that
  fits the event data...
