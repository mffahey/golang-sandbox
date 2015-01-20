/*
 * Package packets contains utility functions for working with TFTP transactions on the packet level
 */
package packets

import (
    "encoding/binary"
    "bytes"
    "errors"
    "fmt"
    "net"
    "time"
    
    "github.com/mffahey/golang-sandbox/tftp/tftpconst"
    "github.com/mffahey/golang-sandbox/tftp/debug"
)

type SourcedPacket struct {
    Data []byte
    Src net.Addr
}

/*
 * Returns as a string the sender TFTP TID of this packet, i.e. the sender's port number
 */
func (p *SourcedPacket) TID() string {
    _, port, err := net.SplitHostPort(p.Src.String())
    if err != nil {
        debug.Debugln("getTid failed for packet with source", p.Src, "Error was:", err)
        return ""
    }
    return port
}

/* 
 * If this is a valid TFTP packet, return the opcode
 */
func (p *SourcedPacket) Opcode() (uint16, error) {
    if len(p.Data) < 2 {
        return 0, errors.New("tftps: packet too short")
    }
    op := uint16(p.Data[0])
    op = op << (8)
    op += uint16(p.Data[1])
    if op > 5 {
        return 0, errors.New(fmt.Sprintf("tftps: invalid opcode %v", op))
    }
    return op, nil
}

/* 
 * If this is a valid RRQ/WRQ TFTP packet, return the request params for filename and transmission mode.
 * Other optional 0-separated params (e.g. tsize) may be present depedning on the client revision, but our server just ignores them.
 */
func (p *SourcedPacket) ReqParams() (filename, tmode string, err error) {
    if len(p.Data) < 6 {
        return "", "", errors.New("tftps: RRQ/WRQ packet too short.")
    }
    rparams := bytes.Split(p.Data[2:], []byte{0})
    if len(rparams) >= 2 {
        filename = string(rparams[0])
        tmode = string(rparams[1])
    } else {
        err = errors.New("tftps: RRQ/WRQ packet improperly formatted")
    }
    return
}

/* 
 * If this is a valid ACK/DATA TFTP packet, return the Block # 
 */
func (p *SourcedPacket) Block() (uint16, error) {
    if len(p.Data) < 4 {
        return 0, errors.New("tftps: ACK/DATA packet too short.")
    }
    block := uint16(p.Data[2])
    block = block << (8)
    block += uint16(p.Data[3])
    return block, nil
}

/* 
 * If this is a valid DATA TFTP packet, return a byte slice corresponding to the data field
 */
func (p *SourcedPacket) ContentData() ([]byte, error) {
    if len(p.Data) < 4 {
        return nil, errors.New("tftps: DATA packet too short.")
    }
    return p.Data[4:], nil
}

// Packet Sending Functions

/* 
 * Use the given conn to send an Data packet in response to packet sp (based on sender address)
 */ 
func WriteDataPacket(conn *net.PacketConn, p *SourcedPacket, block uint16, data []byte, deadline time.Time ) error {
    var b bytes.Buffer
    if err := binary.Write(&b, binary.BigEndian, tftpconst.OpData); err != nil {
        debug.Debugln("writeDataPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    if err := binary.Write(&b, binary.BigEndian, block); err != nil {
        debug.Debugln("writeDataPacket: Error writing block into packet buffer. Error was ", err);
        return err
    }
    b.Write([]byte(data))
    content := (&b).Bytes()
    debug.Debugln("writeDataPacket: content is", string(content));
    (*conn).SetDeadline(deadline)
    if _, err := (*conn).WriteTo(content, p.Src); err != nil {
        debug.Debugln("writeDataPacket: Error sending packet. Error was ", err);
        return err
    }
    return nil
}

/* 
 * Use the given conn to send an Ack packet in response to packet sp (based on sender address)
 */ 
func WriteAckPacket(conn *net.PacketConn, p *SourcedPacket,  block uint16, deadline time.Time ) error {
    var b bytes.Buffer
    if err := binary.Write(&b, binary.BigEndian, tftpconst.OpAck); err != nil {
        debug.Debugln("writeAckPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    if err := binary.Write(&b, binary.BigEndian, block); err != nil {
        debug.Debugln("writeAckPacket: Error writing block into packet buffer. Error was ", err);
        return err
    }
    content := (&b).Bytes()
    debug.Debugln("writeAckPacket: content is", string(content));
    (*conn).SetDeadline(deadline)
    if _, err := (*conn).WriteTo(content, p.Src); err != nil {
        debug.Debugln("writeAckPacket: Error sending packet. Error was ", err);
        return err
    }
    return nil
}

/* 
 * Use the given conn to send an Error packet in response to packet sp (based on sender address)
 */ 
func WriteErrorPacket(conn *net.PacketConn, p *SourcedPacket, errCode uint16, errMsg string, deadline time.Time ) error {
    var b bytes.Buffer
    if err := binary.Write(&b, binary.BigEndian, tftpconst.OpErr); err != nil {
        debug.Debugln("writeErrorPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    if err := binary.Write(&b, binary.BigEndian, errCode); err != nil {
        debug.Debugln("writeErrorPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    b.Write([]byte(errMsg))
    b.WriteByte(byte(0))
    content := (&b).Bytes()
    debug.Debugln("writeErrorPacket: content is", string(content));
    (*conn).SetDeadline(deadline)
    if _, err := (*conn).WriteTo(content, p.Src); err != nil {
        debug.Debugln("writeErrorPacket: Error sending packet. Error was ", err);
        return err
    }
    return nil
}