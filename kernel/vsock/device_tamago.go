//go:build tamago && arm64

package vsock

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/usbarmory/tamago/kvm/virtio"
)

const (
	queueDepth       = 32
	packetBufferSize = HeaderSize + MaxPacketPayload
	eventBufferSize  = 4
)

// Device drives a VirtIO-vsock MMIO transport by polling its split queues.
type Device struct {
	Transport virtio.VirtIO

	rx      *virtio.VirtualQueue
	tx      *virtio.VirtualQueue
	event   *virtio.VirtualQueue
	txDepth uint16
	txSent  uint16
	cid     uint64
	started bool
}

func (d *Device) initQueue(index, length int, flags uint16) (*virtio.VirtualQueue, int, error) {
	size := d.Transport.MaxQueueSize(index)
	if size <= 0 {
		return nil, 0, fmt.Errorf("VirtIO-vsock queue %d unavailable", index)
	}
	if size > queueDepth {
		size = queueDepth
	}
	queue := &virtio.VirtualQueue{}
	queue.Init(size, length, flags)
	d.Transport.SetQueueSize(index, size)
	return queue, size, nil
}

func (d *Device) Init() error {
	if d.Transport == nil {
		return errors.New("missing VirtIO-vsock transport")
	}
	if err := d.Transport.Init(FeatureVersion1); err != nil {
		return err
	}
	if id := d.Transport.DeviceID(); id != DeviceID {
		return fmt.Errorf("incompatible device ID (%x != %x)", id, DeviceID)
	}
	if d.Transport.NegotiatedFeatures()&FeatureVersion1 == 0 {
		return errors.New("VirtIO-vsock device did not negotiate VERSION_1")
	}
	for index := 0; index <= EventQueue; index++ {
		if d.Transport.QueueReady(index) {
			return fmt.Errorf("VirtIO-vsock queue %d already in use", index)
		}
	}

	config := d.Transport.Config(8)
	if len(config) != 8 {
		return errors.New("invalid VirtIO-vsock configuration")
	}
	d.cid = binary.LittleEndian.Uint64(config)
	if d.cid == 0 {
		return errors.New("invalid VirtIO-vsock guest CID")
	}

	var err error
	d.rx, _, err = d.initQueue(ReceiveQueue, packetBufferSize, virtio.Write)
	if err != nil {
		return err
	}
	var txDepth int
	d.tx, txDepth, err = d.initQueue(TransmitQueue, packetBufferSize, 0)
	if err != nil {
		return err
	}
	d.txDepth = uint16(txDepth)
	d.event, _, err = d.initQueue(EventQueue, eventBufferSize, virtio.Write)
	return err
}

func (d *Device) CID() uint64 {
	return d.cid
}

// Start activates the VirtIO-vsock queues. It is separate from Serve so the
// control-plane transport can reach DRIVER_OK before optional guest devices
// are initialized.
func (d *Device) Start() error {
	if d.rx == nil || d.tx == nil || d.event == nil {
		return errors.New("VirtIO-vsock device is not initialized")
	}
	if d.started {
		return nil
	}
	d.Transport.SetQueue(ReceiveQueue, d.rx)
	d.Transport.SetQueue(TransmitQueue, d.tx)
	d.Transport.SetQueue(EventQueue, d.event)
	d.Transport.SetReady()
	d.Transport.QueueNotify(ReceiveQueue)
	d.Transport.QueueNotify(EventQueue)
	d.started = true
	return nil
}

func (d *Device) txAvailable() bool {
	return uint16(d.txSent-d.tx.Used.Index()) < d.txDepth
}

// Serve listens on port and dispatches complete HTTP requests to handler.
func (d *Device) Serve(port uint32, handler func([]byte) []byte) error {
	if err := d.Start(); err != nil {
		return err
	}
	endpoint := NewEndpoint(d.cid, port, handler)
	pending := [][]byte{}
	rxBuffer := make([]byte, packetBufferSize)
	eventBuffer := make([]byte, eventBufferSize)

	for {
		progress := false
		rxReplenished := false
		for {
			n, err := d.rx.Pop(rxBuffer)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			progress = true
			rxReplenished = true
			packets, err := endpoint.Handle(rxBuffer[:n])
			if err != nil {
				return err
			}
			pending = append(pending, packets...)
		}
		if rxReplenished {
			d.Transport.QueueNotify(ReceiveQueue)
		}

		eventReplenished := false
		for {
			n, err := d.event.Pop(eventBuffer)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			progress = true
			eventReplenished = true
			if n >= 4 && binary.LittleEndian.Uint32(eventBuffer[:4]) == 0 {
				endpoint.Reset()
			}
		}
		if eventReplenished {
			d.Transport.QueueNotify(EventQueue)
		}

		pending = append(pending, endpoint.Drain(8)...)
		for len(pending) > 0 && d.txAvailable() {
			d.tx.Push(pending[0])
			pending = pending[1:]
			d.txSent++
			d.Transport.QueueNotify(TransmitQueue)
			progress = true
		}

		if !progress {
			time.Sleep(200 * time.Microsecond)
		}
	}
}
