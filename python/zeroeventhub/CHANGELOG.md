# ZeroEventHub Changelog

## v2.0

Switched from `requests` to `httpx` due to a memory leak in `requests` with long-lived sessions and many requests.
- As a result, we are now using an `AsyncClient` which is the `httpx` equivalent of a `requests.Session`. After observing usage patterns, it was noted that the session was always passed in as a parameter, due to consuming feeds which require authentication, so `AsyncClient` has been made mandatory instead of optional to simplify the code a bit.
- It was also observed that it can be useful to directly receive the parsed response as it is streamed from the server instead of being forced to use an `EventReceiver` protocol. This makes it easier to process received events in batches. As the above dependency change is a breaking change anyway, and due to the switch to async calls, it was the perfect time to switch the `Client.fetch_events` method to return an `AsyncGenerator`.  A helper method called `receive_events` has been introduced to make it easy to keep the `EventReceiver` behavior from before.
