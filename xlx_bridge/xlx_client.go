package main

// XLXClient abstracts the DCS and DExtra protocols for XLX reflector connections.
type XLXClient interface {
	Connect() error
	Close()
	Done() <-chan struct{}
	RunReader()
	RunKeepalive()
	SetVoiceCallback(cb func(frame *DCSVoiceFrame))
	SetTXOrigin(callsign string)
	StartTX()
	StopTX() error
	SendVoice(ambe [9]byte) error
	TXStreamID() uint16
}
