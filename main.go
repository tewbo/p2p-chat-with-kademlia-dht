package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/libp2p/go-libp2p"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	gossipsub "github.com/libp2p/go-libp2p-pubsub"
	hostcore "github.com/libp2p/go-libp2p/core/host"
	peerid "github.com/libp2p/go-libp2p/core/peer"
	routingDiscovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
)

type messagePayload struct {
	Text   string    `json:"text"`
	Sender peerid.ID `json:"sender_id"`
	Name   string    `json:"sender_name"`
}

type profile struct {
	ID   peerid.ID
	Nick string
}

var (
	topicFlag = flag.String("ch", "my_room", "Chat room channel")
	nickFlag  = flag.String("nick", "", "User nickname")
	self      profile
)

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
		libp2p.EnableRelay(),
		libp2p.DefaultTransports,
	)
	if err != nil {
		log.Fatalln("[P2P] Node initialization error:", err)
	}

	self.ID = h.ID()
	self.Nick = *nickFlag
	if self.Nick == "" {
		tag := self.ID.String()
		self.Nick = fmt.Sprintf("%s-%s", os.Getenv("USER"), tag[len(tag)-8:])
	}

	go searchNeighbors(ctx, h, *topicFlag)

	ps, err := gossipsub.NewGossipSub(ctx, h)
	if err != nil {
		log.Fatalln("[PubSub] Creation failed:", err)
	}

	room, err := ps.Join(*topicFlag)
	if err != nil {
		log.Fatalln("Cannot join chat topic:", err)
	}
	subscription, err := room.Subscribe()
	if err != nil {
		log.Fatalln("Cannot subscribe to topic:", err)
	}

	go inputHandler(ctx, room)
	messageListener(ctx, subscription)
}

func searchNeighbors(ctx context.Context, h hostcore.Host, topic string) {
	dhtInstance := bootstrapDHT(ctx, h)
	disc := routingDiscovery.NewRoutingDiscovery(dhtInstance)
	discoveryUtil.Advertise(ctx, disc, topic)

	found := false
	for !found {
		fmt.Println("Looking for other nodes...")
		peerch, err := disc.FindPeers(ctx, topic)
		if err != nil {
			log.Fatalf("Discovery failed: %v", err)
		}
		for p := range peerch {
			if p.ID == h.ID() {
				continue
			}
			if err := h.Connect(ctx, p); err == nil {
				fmt.Println("Connected to peer:", p.ID)
				found = true
			}
		}
	}
	fmt.Println("Peer discovery completed")
}

func bootstrapDHT(ctx context.Context, h hostcore.Host) *kaddht.IpfsDHT {
	instance, err := kaddht.New(ctx, h)
	if err != nil {
		log.Fatalln("DHT setup error:", err)
	}
	if err := instance.Bootstrap(ctx); err != nil {
		log.Fatalln("DHT bootstrap error:", err)
	}
	var wg sync.WaitGroup
	for _, addr := range kaddht.DefaultBootstrapPeers {
		info, _ := peerid.AddrInfoFromP2pAddr(addr)
		wg.Add(1)
		go func(pi peerid.AddrInfo) {
			defer wg.Done()
			if err := h.Connect(ctx, pi); err != nil {
				log.Println("DHT bootstrap warning:", err)
			}
		}(*info)
	}
	wg.Wait()
	return instance
}

func inputHandler(ctx context.Context, topic *gossipsub.Topic) {
	fmt.Println("P2P chat started.")
	scanner := bufio.NewReader(os.Stdin)
	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			log.Println("Input error:", err)
			continue
		}
		data, err := json.Marshal(messagePayload{Text: line, Sender: self.ID, Name: self.Nick})
		if err != nil {
			log.Println("Encoding failure:", err)
			continue
		}
		if err := topic.Publish(ctx, data); err != nil {
			log.Println("Failed to send message:", err)
		}
	}
}

func messageListener(ctx context.Context, sub *gossipsub.Subscription) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			log.Println("Subscription read error:", err)
			continue
		}
		var payload messagePayload
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			log.Println("Decoding error:", err)
			continue
		}
		fmt.Printf("%s: %s", payload.Name, payload.Text)
	}
}
