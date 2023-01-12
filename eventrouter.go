package eventrouter

import (
	"context"
)

/*
This function returns a function to send messages to a set of subscriptions, and a means of registering a subscription (and later cancelling iItemType)
It has a cache (kept in RAM) for buffered operations.
It uses generics, so this should be usable for items of any type
Setting maxcachedepth to <1 leaves it unbounded.
*/

type Router[ItemType any] struct {
	Send      func(item ItemType)                                   //Submit an item to the router
	Tail      func(ctx context.Context, onItem func(item ItemType)) //Get any cached items, and all future submissions. Equivalent to Items() and Subscribe()
	Subscribe func(ctx context.Context, onItem func(item ItemType)) //Get all future submissions
	Items     func() []ItemType                                     //Get the current list of items cached
	Clear     func()                                                //Clear the item cache
	Count     func() int                                            //Count the items in the cache
}

func New[ItemType any](ctx context.Context, cacheDepth int) (router Router[ItemType]) {

	var ( //The API to the "server"
		subscribeC = make(chan subItemsReq[ItemType])
		sendC      = make(chan sendItemReq[ItemType])
		cancelSubC = make(chan cancelSubReq[ItemType])
		countC     = make(chan countItemsReq[ItemType])
		clearC     = make(chan clearItemsReq[ItemType])
	)

	go func() { //All routing happens in this goroutine. Think of it as a webserver that responds, one at a time, to requests and dispatches responses
		var ( //The guarded internal state. All mutation and observation happens through req/res pattern
			cache         []ItemType
			nextSubId     = 1000
			subscriptions = make(map[int]chan updateMessage[ItemType])
		)
		for {
			select {
			//Done
			case <-ctx.Done():
				return
			//Cancel
			case req := <-cancelSubC:
				delete(subscriptions, req.id)
				req.resp <- cancelSubRes[ItemType]{}
			//Subscribe
			case req := <-subscribeC:
				subId := nextSubId
				subscriptions[subId] = make(chan updateMessage[ItemType])
				resp := subItemsRes[ItemType]{
					id:      subId,
					updateC: subscriptions[subId],
					cancel: func() {
						cancelReq := cancelSubReq[ItemType]{
							id:   subId,
							resp: make(chan cancelSubRes[ItemType]),
						}

						cancelSubC <- cancelReq
						<-cancelReq.resp
					},
				}
				req.resp <- resp

				if req.tail {
					for _, item := range cache {
						resp.updateC <- updateMessage[ItemType]{item: item}
					}
				}
				nextSubId++
			//Send
			case req := <-sendC:
				for _, sub := range subscriptions {
					sub <- updateMessage[ItemType]{item: req.item}
				}
				if cacheDepth != 0 {
					if overflow := len(cache) + 1 - cacheDepth; overflow > 0 {
						cache = cache[overflow-1:]
					}
				}
				cache = append(cache, req.item)
				req.resp <- sendItemRes[ItemType]{}
			case req := <-countC:
				req.resp <- countItemsRes[ItemType]{count: len(cache)}
			//Clear
			case req := <-clearC:
				cache = cache[:0]
				req.resp <- clearItemsRes[ItemType]{}
			}
		}
	}()

	router.Send = func(item ItemType) {
		req := sendItemReq[ItemType]{item: item, resp: make(chan sendItemRes[ItemType])}
		sendC <- req
		<-req.resp
	}

	//Call the server and subscribe to the router
	sub := func(ctx context.Context, tail bool, onItem func(item ItemType)) {
		sr := subItemsReq[ItemType]{
			resp: make(chan subItemsRes[ItemType]),
			tail: tail,
		}
		subscribeC <- sr //issue a req
		resp := <-sr.resp
		go func() {
			for {
				select {
				case <-ctx.Done():
					resp.cancel()
					return
				case update := <-resp.updateC:
					onItem(update.item)
				}
			}
		}()
	}
	//Public API
	router.Tail = func(ctx context.Context, onItem func(item ItemType)) { sub(ctx, true, onItem) }
	router.Subscribe = func(ctx context.Context, onItem func(item ItemType)) { sub(ctx, false, onItem) }
	router.Items = func() []ItemType {
		req := getItemsReq[ItemType]{resp: make(chan getItemsRes[ItemType])}
		res := <-req.resp
		return res.items
	}
	router.Clear = func() {
		req := clearItemsReq[ItemType]{resp: make(chan clearItemsRes[ItemType])}
		<-req.resp
	}
	router.Count = func() int {
		req := countItemsReq[ItemType]{resp: make(chan countItemsRes[ItemType])}
		resp := <-req.resp
		return resp.count
	}
	return router
}

//the request and response datatypes used to interact with the core server

type countItemsRes[ItemType any] struct{ count int }
type countItemsReq[ItemType any] struct {
	resp chan countItemsRes[ItemType]
}
type sendItemRes[ItemType any] struct{}
type sendItemReq[ItemType any] struct {
	item ItemType
	resp chan sendItemRes[ItemType]
}

type updateMessage[ItemType any] struct{ item ItemType }
type subItemsRes[ItemType any] struct {
	id      int
	tail    []ItemType
	filter  func(ItemType) bool //return true for interested
	updateC chan updateMessage[ItemType]
	cancel  func()
}
type subItemsReq[ItemType any] struct {
	tail bool
	resp chan subItemsRes[ItemType]
}

type getItemsRes[ItemType any] struct{ items []ItemType }
type getItemsReq[ItemType any] struct{ resp chan getItemsRes[ItemType] }

type clearItemsRes[ItemType any] struct{}
type clearItemsReq[ItemType any] struct{ resp chan clearItemsRes[ItemType] }

type cancelSubRes[ItemType any] struct{}
type cancelSubReq[ItemType any] struct {
	id   int
	resp chan cancelSubRes[ItemType]
}
