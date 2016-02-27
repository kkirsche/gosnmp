// Copyright 2012 Andreas Louca. All rights reserved.
// Use of this source code is goverend by a BSD-style
// license that can be found in the LICENSE file.

package gosnmp

import (
	"fmt"
	l "github.com/alouca/gologger"
	"net"
	"strings"
	"time"
)

// Conn allows for us to send the correct SNMP requests based on what the
// user has requested
type Conn struct {
	Target    string
	Community string
	Version   SnmpVersion
	Timeout   time.Duration
	conn      net.Conn
	Log       *l.Logger
}

// DefaultPort is the default SNMP port
var DefaultPort = 161

// Connect creates a new SNMP Client. Target is the IP address, Community the
// SNMP Community String and Version the SNMP version. Currently only v2c is
// supported. Timeout parameter is measured in seconds.
func Connect(target, community string, version SnmpVersion, timeout int64) (*Conn, error) {
	if !strings.Contains(target, ":") {
		target = fmt.Sprintf("%s:%d", target, DefaultPort)
	}

	// Open a UDP connection to the target
	conn, err := net.DialTimeout("udp", target, time.Duration(timeout)*time.Second)

	if err != nil {
		return nil, fmt.Errorf("Error establishing connection to host: %s\n", err.Error())
	}
	s := &Conn{target, community, version, time.Duration(timeout) * time.Second, conn, l.CreateLogger(false, false)}

	return s, nil
}

// SetVerbose enables verbose logging
func (x *Conn) SetVerbose(v bool) {
	x.Log.VerboseFlag = v
}

// SetDebug enables debugging
func (x *Conn) SetDebug(d bool) {
	x.Log.DebugFlag = d
}

// SetTimeout sets the timeout for network read/write functions.
// Defaults to 5 seconds.
func (x *Conn) SetTimeout(seconds int64) {
	if seconds <= 0 {
		seconds = 5
	}
	x.Timeout = time.Duration(seconds) * time.Second
}

// StreamWalk will start walking a specified OID, and push through a channel the results
// as it receives them, without waiting for the whole process to finish to return the
// results. Once it has completed the walk, the channel is closed.
func (x *Conn) StreamWalk(oid string, c chan SnmpPDU) error {
	if oid == "" {
		close(c)
		return fmt.Errorf("No OID given\n")
	}

	requestOid := oid

	for {
		res, err := x.GetNext(oid)
		if err != nil {
			close(c)
			return err
		}
		if res != nil {
			if len(res.Variables) > 0 {
				if strings.Index(res.Variables[0].Name, requestOid) > -1 {
					c <- res.Variables[0]
					// Set to the next
					oid = res.Variables[0].Name
					x.Log.Debug("Moving to %s\n", oid)
				} else {
					x.Log.Debug("Root OID mismatch, stopping walk\n")
					break
				}
			} else {
				break
			}
		} else {
			break
		}

	}
	close(c)
	return nil
}

// StreamBulkWalk will start bulk walking a specified OID, and push through a
// channel the results as it receives them, without waiting for the whole
// process to finish to return the results. Once it has completed the walk,
// the channel is closed.
func (x *Conn) StreamBulkWalk(maxRepetitions uint8, oid string, resultChan chan *SnmpPDU) error {
	rootOID := oid
	response, err := x.GetBulk(0, maxRepetitions, oid)
	if err != nil {
		close(resultChan)
		return err
	}
	for i, v := range response.Variables {
		if v.Value == "endOfMib" {
			close(resultChan)
			return nil
		}
		// is this variable still in the requested oid range
		if strings.HasPrefix(v.Name, rootOID) {
			resultChan <- &v
			// is the last oid received still in the requested range
			if i == len(response.Variables)-1 {
				var subResults []SnmpPDU
				subResults, err = x._bulkWalk(maxRepetitions, v.Name, rootOID)
				if err != nil {
					close(resultChan)
					return err
				}
				for _, subResult := range subResults {
					resultChan <- &subResult
				}
			}
		}
	}
	close(resultChan)
	return nil
}

func (x *Conn) BulkWalk(maxRepetitions uint8, oid string) (results []SnmpPDU, err error) {
	if oid == "" {
		return nil, fmt.Errorf("No OID given\n")
	}
	return x._bulkWalk(maxRepetitions, oid, oid)
}
func (x *Conn) _bulkWalk(maxRepetitions uint8, searchingOID string, rootOID string) (results []SnmpPDU, err error) {
	response, err := x.GetBulk(0, maxRepetitions, searchingOID)
	if err != nil {
		return
	}
	for i, v := range response.Variables {
		if v.Value == "endOfMib" {
			return
		}
		// is this variable still in the requested oid range
		if strings.HasPrefix(v.Name, rootOID) {
			results = append(results, v)
			// is the last oid received still in the requested range
			if i == len(response.Variables)-1 {
				var subResults []SnmpPDU
				subResults, err = x._bulkWalk(maxRepetitions, v.Name, rootOID)
				if err != nil {
					return
				}
				results = append(results, subResults...)
			}
		}
	}
	return
}

// Walk will SNMP walk the target, blocking until the process is complete
func (x *Conn) Walk(oid string) (results []SnmpPDU, err error) {
	if oid == "" {
		return nil, fmt.Errorf("No OID given\n")
	}
	results = make([]SnmpPDU, 0)
	requestOid := oid

	for {
		res, err := x.GetNext(oid)
		if err != nil {
			return results, err
		}
		if res != nil {
			if len(res.Variables) > 0 {
				if strings.Index(res.Variables[0].Name, requestOid) > -1 {
					results = append(results, res.Variables[0])
					// Set to the next
					oid = res.Variables[0].Name
					x.Log.Debug("Moving to %s\n", oid)
				} else {
					x.Log.Debug("Root OID mismatch, stopping walk\n")
					break
				}
			} else {
				break
			}
		} else {
			break
		}

	}
	return
}

// Marshals & send an SNMP request. Unmarshals the response and returns back the parsed
// SNMP packet
func (x *Conn) sendPacket(packet *SnmpPacket) (*SnmpPacket, error) {
	// Set timeouts on the connection
	deadline := time.Now()
	x.conn.SetDeadline(deadline.Add(x.Timeout))

	// Marshal it
	fBuf, err := packet.marshal()

	if err != nil {
		return nil, err
	}

	// Send the packet!
	_, err = x.conn.Write(fBuf)
	if err != nil {
		return nil, fmt.Errorf("Error writing to socket: %s\n", err.Error())
	}
	// Try to read the response
	resp := make([]byte, 8192, 8192)
	n, err := x.conn.Read(resp)

	if err != nil {
		return nil, fmt.Errorf("Error reading from UDP: %s\n", err.Error())
	}

	// Unmarshal the read bytes
	pdu, err := Unmarshal(resp[:n])

	if err != nil {
		return nil, fmt.Errorf("Unable to decode packet: %s\n", err.Error())
	}

	if len(pdu.Variables) < 1 {
		return nil, fmt.Errorf("No responses received.")
	}

	return pdu, nil
}

// GetNext sends an SNMP Get Next Request to the target.
// Returns the next variable response from the OID given or an error
func (x *Conn) GetNext(oid string) (*SnmpPacket, error) {
	var err error
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	// Create the packet
	packet := new(SnmpPacket)

	packet.Community = x.Community
	packet.Error = 0
	packet.ErrorIndex = 0
	packet.RequestType = GetNextRequest
	packet.Version = 1 // version 2
	packet.Variables = []SnmpPDU{SnmpPDU{Name: oid, Type: Null}}

	return x.sendPacket(packet)
}

// Debug function. Unmarshals raw bytes and returns the result without the network part
func (x *Conn) Debug(data []byte) (*SnmpPacket, error) {
	packet, err := Unmarshal(data)

	if err != nil {
		return nil, fmt.Errorf("Unable to decode packet: %s\n", err.Error())
	}
	return packet, nil
}

// GetBulk sends an SNMP BULK-GET request to the target. Returns a Variable with the response or an error
func (x *Conn) GetBulk(nonRepeaters, maxRepetitions uint8, oids ...string) (*SnmpPacket, error) {
	var err error
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	// Create the packet
	packet := new(SnmpPacket)

	packet.Community = x.Community
	packet.NonRepeaters = nonRepeaters
	packet.MaxRepetitions = maxRepetitions
	packet.RequestType = GetBulkRequest
	packet.Version = 1 // version 2
	packet.Variables = make([]SnmpPDU, len(oids))

	for i, oid := range oids {
		packet.Variables[i] = SnmpPDU{Name: oid, Type: Null}
	}

	return x.sendPacket(packet)
}

// Get sends an SNMP GET request to the target. Returns a Variable with the response or an error
func (x *Conn) Get(oid string) (*SnmpPacket, error) {
	var err error
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	// Create the packet
	packet := new(SnmpPacket)

	packet.Community = x.Community
	packet.Error = 0
	packet.ErrorIndex = 0
	packet.RequestType = GetRequest
	packet.Version = 1 // version 2
	packet.Variables = []SnmpPDU{SnmpPDU{Name: oid, Type: Null}}

	return x.sendPacket(packet)
}

// GetMulti sends an SNMP GET request to the target. Returns a Variable with the response or an error
func (x *Conn) GetMulti(oids []string) (*SnmpPacket, error) {
	var err error
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	// Create the packet
	packet := new(SnmpPacket)

	packet.Community = x.Community
	packet.Error = 0
	packet.ErrorIndex = 0
	packet.RequestType = GetRequest
	packet.Version = 1 // version 2
	packet.Variables = make([]SnmpPDU, len(oids))

	for i, oid := range oids {
		packet.Variables[i] = SnmpPDU{Name: oid, Type: Null}
	}

	return x.sendPacket(packet)
}
