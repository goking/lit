package qln

import (
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcutil"
	"github.com/mit-dci/lit/lndc"
	"github.com/mit-dci/lit/lnutil"
	"github.com/mit-dci/lit/portxo"
)

// TCPListener starts a litNode listening for incoming LNDC connections
func (nd *LitNode) TCPListener(
	lisIpPort string) (*btcutil.AddressWitnessPubKeyHash, error) {
	idPriv := nd.IdKey()
	listener, err := lndc.NewListener(nd.IdKey(), lisIpPort)
	if err != nil {
		return nil, err
	}

	myId := btcutil.Hash160(idPriv.PubKey().SerializeCompressed())
	lisAdr, err := btcutil.NewAddressWitnessPubKeyHash(myId, nd.Param)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Listening on %s\n", listener.Addr().String())
	fmt.Printf("Listening with base58 address: %s lnid: %x\n",
		lisAdr.String(), myId[:16])

	go func() {
		for {
			netConn, err := listener.Accept() // this blocks
			if err != nil {
				log.Printf("Listener error: %s\n", err.Error())
				continue
			}
			newConn, ok := netConn.(*lndc.LNDConn)
			if !ok {
				fmt.Printf("Got something that wasn't a LNDC")
				continue
			}
			fmt.Printf("Incomming connection from %x on %s\n",
				newConn.RemotePub.SerializeCompressed(), newConn.RemoteAddr().String())

			// don't save host/port for incomming connections
			peerIdx, err := nd.GetPeerIdx(newConn.RemotePub, "")
			if err != nil {
				log.Printf("Listener error: %s\n", err.Error())
				continue
			}

			nd.RemoteMtx.Lock()
			var peer RemotePeer
			peer.Idx = peerIdx
			peer.Con = newConn
			nd.RemoteCons[peerIdx] = &peer
			nd.RemoteMtx.Unlock()

			// each connection to a peer gets its own LNDCReader
			go nd.LNDCReader(&peer)
		}
	}()
	return lisAdr, nil
}

// DialPeer makes an outgoing connection to another node.
func (nd *LitNode) DialPeer(lnAdr *lndc.LNAdr) error {
	// get my private ID key
	idPriv := nd.IdKey()

	// Assign remote connection
	newConn := new(lndc.LNDConn)

	var id []byte
	if lnAdr.PubKey != nil {
		id = lnAdr.PubKey.SerializeCompressed()
	} else {
		id = lnAdr.Base58Adr.ScriptAddress()
	}

	err := newConn.Dial(idPriv, lnAdr.NetAddr.String(), id)
	if err != nil {
		return err
	}

	// if connect is successful, either query for already existing peer index, or
	// if the peer is new, make an new index, and save the hostname&port

	// figure out peer index, or assign new one for new peer.  Since
	// we're connecting out, also specify the hostname&port
	peerIdx, err := nd.GetPeerIdx(newConn.RemotePub, newConn.RemoteAddr().String())
	if err != nil {
		return err
	}

	nd.RemoteMtx.Lock()
	var p RemotePeer
	p.Con = newConn
	p.Idx = peerIdx
	nd.RemoteCons[peerIdx] = &p
	nd.RemoteMtx.Unlock()

	// each connection to a peer gets its own LNDCReader
	go nd.LNDCReader(&p)

	return nil
}

// OutMessager takes messages from the outbox and sends them to the ether. net.
func (nd *LitNode) OutMessager() {
	for {
		msg := <-nd.OmniOut
		if !nd.ConnectedToPeer(msg.PeerIdx) {
			fmt.Printf("message type %x to peer %d but not connected\n",
				msg.MsgType, msg.PeerIdx)
			continue
		}

		rawmsg := append([]byte{msg.MsgType}, msg.Data...)
		nd.RemoteMtx.Lock() // not sure this is needed...
		n, err := nd.RemoteCons[msg.PeerIdx].Con.Write(rawmsg)
		if err != nil {
			fmt.Printf("error writing to peer %d: %s\n", msg.PeerIdx, err.Error())
		} else {
			fmt.Printf("type %x %d bytes to peer %d\n", msg.MsgType, n, msg.PeerIdx)
		}
		nd.RemoteMtx.Unlock()
	}
}

type PeerInfo struct {
	PeerNumber uint32
	RemoteHost string
}

func (nd *LitNode) GetConnectedPeerList() []PeerInfo {
	nd.RemoteMtx.Lock()
	nd.RemoteMtx.Unlock()
	var peers []PeerInfo
	for k, v := range nd.RemoteCons {
		var newPeer PeerInfo
		newPeer.PeerNumber = k
		newPeer.RemoteHost = v.Con.RemoteAddr().String()
		peers = append(peers, newPeer)
	}
	return peers
}

// ConnectedToPeer checks whether you're connected to a specific peer
func (nd *LitNode) ConnectedToPeer(peer uint32) bool {
	nd.RemoteMtx.Lock()
	_, ok := nd.RemoteCons[peer]
	nd.RemoteMtx.Unlock()
	return ok
}

// IdKey returns the identity private key
func (nd *LitNode) IdKey() *btcec.PrivateKey {
	var kg portxo.KeyGen
	kg.Depth = 5
	kg.Step[0] = 44 | 1<<31
	kg.Step[1] = 0 | 1<<31
	kg.Step[2] = 9 | 1<<31
	kg.Step[3] = 0 | 1<<31
	kg.Step[4] = 0 | 1<<31
	return nd.BaseWallet.GetPriv(kg)
}

// SendChat sends a text string to a peer
func (nd *LitNode) SendChat(peer uint32, chat string) error {
	if !nd.ConnectedToPeer(peer) {
		return fmt.Errorf("Not connected to peer %d", peer)
	}

	outMsg := new(lnutil.LitMsg)
	outMsg.MsgType = lnutil.MSGID_TEXTCHAT
	outMsg.PeerIdx = peer
	outMsg.Data = []byte(chat)
	nd.OmniOut <- outMsg

	return nil
}
