package main

import (
    "encoding/binary"
    "bytes"
    "errors"
    "fmt"
    "log"
    "net"
    "os"
    "runtime"
    "strings"
    "sync"
    "time"
)

const (
    // local listener ip address (the port for the initial request listener)
    laddr = ":69"
    // default packet read length in bytes. Must be long enough to accommodate the longest possible packet, since a UDP PacketConn may throw away any leftover bytes
    pLen = 2048
    blockSize = 512
    
    // default ack timeout for sent packets (10 seconds)
    timeoutDuration = 10000 * time.Millisecond
    // default retry count for un-acked packets 
    retry = 2
    
    // op code constants
    opRrq uint16 = 1
    opWrq uint16 = 2
    opData uint16 = 3
    opAck uint16 = 4
    opErr uint16 = 5
    
    // error code constants
    errNDef uint16 = 0
    errFNF uint16 = 1
    errAccess uint16 = 2
    errDiskFull uint16 = 3
    errIllegalOp uint16 = 4
    errUnknownTID uint16 = 5
    errFileExists uint16 = 6
    
)

func timeout() time.Time {
    return time.Now().Add(timeoutDuration);
}

// FileSystem stuff. Intentionally creating out own file interface instead of using the os.File struct.
// Out File itself implements ReaderWriter to access the underlying data
//TODO: Refactor into its own package later
type InMemFS map[string]*InMemFile

type InMemFile struct {
    lock *sync.RWMutex
    data []byte
}

func (f *InMemFile) isFinished() bool {
    return f.data != nil
}

// sourcedPacket type and methods

type sourcedPacket struct {
    data []byte
    src net.Addr
}

/*
 * Returns as a string the sender TFTP TID of this packet, i.e. the sender's port number
 */
func (p *sourcedPacket) getTID() string {
    _, port, err := net.SplitHostPort(p.src.String())
    if err != nil {
        debugln("getTid failed for packet with source", p.src, "Error was:", err)
        return ""
    }
    return port
}

/* 
 * If this is a valid TFTP packet, return the opcode
 */
func (p *sourcedPacket) getOpcode() (uint16, error) {
    if len(p.data) < 2 {
        return 0, errors.New("tftps: packet too short")
    }
    op := uint16(p.data[0])
    op = op << (8)
    op += uint16(p.data[1])
    if op > 5 {
        return 0, errors.New(fmt.Sprintf("tftps: invalid opcode %v", op))
    }
    return op, nil
}

/* 
 * If this is a valid RRQ/WRQ TFTP packet, return the request params for filename and transmission mode.
 * Other optional 0-separated params (e.g. tsize) may be present depedning on the client revision, but our server just ignores them.
 */
func (p *sourcedPacket) getReqParams() (filename, tmode string, err error) {
    if len(p.data) < 6 {
        return "", "", errors.New("tftps: RRQ/WRQ packet too short.")
    }
    rparams := bytes.Split(p.data[2:], []byte{0})
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
func (p *sourcedPacket) getBlock() (uint16, error) {
    if len(p.data) < 4 {
        return 0, errors.New("tftps: ACK/DATA packet too short.")
    }
    block := uint16(p.data[2])
    block = block << (8)
    block += uint16(p.data[3])
    return block, nil
}

/* 
 * If this is a valid DATA TFTP packet, return a byte slice corresponding to the data field
 */
func (p *sourcedPacket) getData() ([]byte, error) {
    if len(p.data) < 4 {
        return nil, errors.New("tftps: DATA packet too short.")
    }
    return p.data[4:], nil
}

/* 
 * Function which listens for requests, initializes request handlers, and routes incoming packets
 */
func listen(fs *InMemFS, quit chan bool) error {
    fmt.Printf("listen() invoked \n")
    // Open a net.PacketConn. This will be the default request listener
    conn , err := net.ListenPacket("udp", laddr)
    if err != nil {
        log.Fatalf("Unable to open connection on %v ; error was %v\n", laddr, err)
    }
    defer conn.Close()
    packets := make(chan *sourcedPacket)
    go listenPackets(&conn, packets)
    
    fmt.Printf("Listening on %v...", laddr)
    // WaitGroup for tracking whether there are any outstanding requests
    var transWG sync.WaitGroup
    for {
        select {
            case p := <- packets :
                debugln("listen: Packet popped was", string(p.data), "from", p.src);
                transWG.Add(1)
                go func() {
                    defer transWG.Done()
                    handleTransaction(p, fs)
                } ()
            case <-quit :
                fmt.Printf("Waiting for outstanding transactions to complete...")
                transWG.Wait()
                return nil
        }
        
        // handle 
    }
}

// Request handler functions


/* 
 * Using the given PacketConn, continuously read incoming packets and push them into the provided channel
 */
func listenPackets(conn *net.PacketConn, packets chan *sourcedPacket) {
    // Read with no timeout
    err := (*conn).SetReadDeadline(time.Time{})
    if err != nil {
        log.Fatalf("Unable to set read timeout on primary connection %v ; error was %v\n", conn, err)
    }
    pContent := make([]byte, pLen)
    for {
        n, src ,err := (*conn).ReadFrom(pContent)
        if err != nil {
            log.Fatalf("Encountered error reading packet on primary connection %v ; error was %v\n", conn, err)
        }
        debugln(n, " bytes read. Source address was ", src)
        debugln("Content:", string(pContent[:n]))
        packets <- &sourcedPacket{pContent[:n], src}
    }
}

/*
 * Handles the scope of a single transaction, whether read, write, or other
 */
func handleTransaction(p *sourcedPacket, fs *InMemFS) {
    // Get new net.PacketConn for this transaction
    // ":0" to delegate un-socketed port assignment to the OS -- Attr. a hot tip from Justen Meden
    tConn , err := net.ListenPacket("udp", ":0")
    if err != nil {
        fmt.Printf("Unable to open network connection ; error was %v\n", err)
        // This request will be discarded. Caller will need to retry.
        // Open Item: Should we fatal instead of continuing?
        return
    }
    defer tConn.Close()
    err = tConn.SetDeadline(timeout())
    if err != nil {
        debugln("handleTransaction: Unable to set read timeout. Error was:", err);
        writeErrorPacket(&tConn, p, errIllegalOp, err.Error())
        return
    }
    op, err := p.getOpcode()
    if err != nil {
        debugln("handleTransaction: Invalid packet. Ignoring. Error was:", err);
        writeErrorPacket(&tConn, p, errIllegalOp, err.Error())
        return
    }
    switch op {
        case opRrq:
            debugln("handleTransaction: Handling RRQ request.");
            handleRead(&tConn, p, fs)
        case opWrq: 
            debugln("handleTransaction: Handling WRQ request.");
            handleWrite(&tConn, p, fs)
        default: 
            errMsg := fmt.Sprintf("Opcode %v is invalid in this context.", op)
            debugln("handleTransaction:", errMsg, "Sending ERROR response.")
            writeErrorPacket(&tConn, p, errIllegalOp, errMsg)
    }
}

func handleRead(conn *net.PacketConn, p *sourcedPacket, fs *InMemFS) {
    filename, tmode, err := p.getReqParams()
    if err != nil {
        debugln("handleRead: Invalid packet. Error was:", err)
        writeErrorPacket(conn, p, errIllegalOp, err.Error())
        return
    }
    if "octet" != strings.ToLower(tmode) {
        errMsg := fmt.Sprintf("Unsupported TFTP mode %v. Only octet mode is supported by this server.", tmode)
        debugln("handleRead: ", errMsg)
        writeErrorPacket(conn, p, errNDef, errMsg)
        return
    }
    file, exists := (*fs)[filename] 
    if !exists || !file.isFinished() {
        errMsg := fmt.Sprintf("No file exists for filename: %v", filename)
        debugln("handleRead: ", errMsg)
        writeErrorPacket(conn, p, errNDef, errMsg)
        return
    }
    if lockFileForRead {
        file.lock.RLock()
        defer file.lock.RUnlock()
    }
    fileDataBuf := &bytes.Buffer{}
    fileDataBuf.Write(file.data)
    
    pData := make([]byte, blockSize)
    block := uint16(1)
    prevPacketRec := p
    retryCount := 0
    for pDataLen := blockSize; pDataLen == blockSize ; {
        n, err := fileDataBuf.Read(pData)
        if err != nil {
            debugln("handleRead:", err)
            writeErrorPacket(conn, prevPacketRec, errNDef, err.Error())
            return
        }
        pDataLen = n
        for err = writeDataPacket(conn, prevPacketRec, block, pData[:pDataLen]); err != nil ; {
            if retryCount < retry {
                retryCount++
                debugln("handleRead: Retrying packet send for block", block)
                err = writeDataPacket(conn, prevPacketRec, block, pData[:pDataLen])
            } else {
                debugln("handleRead: ", err)
                writeErrorPacket(conn, prevPacketRec, errNDef, err.Error())
                return 
            }
        }
        // Data packet sent. Wait for ack.
        pContent := make([]byte, pLen)
        for ackReceived := false; ackReceived ; {
            (*conn).SetDeadline(timeout())
            n, src ,err := (*conn).ReadFrom(pContent)
            if err != nil {
                e, ok := err.(net.Error)
                if ok && e.Timeout() == true && retryCount < retry {
                    retryCount++
                    debugln("handleRead: timed out waiting for client. Retrying send for block", block)
                    writeDataPacket(conn, prevPacketRec, block, pData[:pDataLen])
                    continue
                }
                debugln("handleRead: ", err)
                writeErrorPacket(conn, prevPacketRec, errNDef, err.Error())
                return 
            }
            packetRec := &sourcedPacket{pContent[:n], src}
            // Check sender TID correctness
            if (packetRec.getTID() != p.getTID()) {
                errMsg := fmt.Sprintf("Unrecognized sender TID %v", filename)
                debugln("handleRead: ", errMsg)
                writeErrorPacket(conn, packetRec, 5, errMsg)
                continue
            }
            // Check OpCode correctness
            if op, _ := packetRec.getOpcode(); op != opAck {
                errMsg := fmt.Sprintf("Opcode %v is invalid in this context.", op)
                debugln("handleRead:", errMsg)
                writeErrorPacket(conn, packetRec, errIllegalOp, errMsg)
                return
            } 
            // CHeck Block correctness (duplicate or next block)
            pBlock, err := packetRec.getBlock()
            if err != nil {
                debugln("handleRead:", err)
                writeErrorPacket(conn, packetRec, errNDef, err.Error())
                return
            }
            if pBlock < block {
                // This could be an old duplicate that was lost in the network. Ignoring. 
                continue;
            } else if pBlock != block {
                errMsg := fmt.Sprintf("Unexpected block # %v; was expecting %v", pBlock, block)
                debugln("handleRead:", errMsg)
                writeErrorPacket(conn, packetRec, errNDef, errMsg)
                return
            }
            
            ackReceived = true
            prevPacketRec = packetRec
        }
        block++
        retryCount = 0
    }
    debugln("handleRead: complete read transaction for file", filename)
    return
}

func handleWrite(conn *net.PacketConn, p *sourcedPacket, fs *InMemFS) {
    filename, tmode, err := p.getReqParams()
    if err != nil {
        debugln("handleWrite: Invalid packet. Error was:", err)
        writeErrorPacket(conn, p, errIllegalOp, err.Error())
        return
    }
    if "octet" != strings.ToLower(tmode) {
        errMsg := fmt.Sprintf("Unsupported TFTP mode %v. Only octet mode is supported by this server.", tmode)
        debugln("handleWrite: ", errMsg)
        writeErrorPacket(conn, p, errNDef, errMsg)
        return
    }
    file, exists := (*fs)[filename] 
    if exists {
        file.lock.Lock()
        defer file.lock.Unlock()
        if !overwriteEnabled && file.isFinished() {
            errMsg := fmt.Sprintf("A file already exists for filename: %v", filename)
            debugln("handleWrite: ", errMsg)
            writeErrorPacket(conn, p, errFileExists, errMsg)
            return
        }
    } else {
        var lock sync.RWMutex
        (&lock).Lock()
        defer (&lock).Unlock()
        file = &InMemFile{&lock, []byte(nil)}
    }
    // put the unfinished file now to prevent duplicate writes
    // if this request errors out, the unfinished file will stay in the map until it is cleaned up.
    (*fs)[filename] = file
    
    newFileDataBuf := &bytes.Buffer{}
    pContent := make([]byte, pLen)
    block := uint16(0)
    prevPacketRec := p
    retryCount := 0
    
    // Write the first Ack. This block occurs a few times below and could probably be factored into a separate func
    for err:= writeAckPacket(conn, prevPacketRec, block); err != nil ; {
        if retryCount < retry {
            retryCount++
            debugln("handleWrite: Retrying packet send for block", block)
            err = writeAckPacket(conn, prevPacketRec, block)
        } else {
            debugln("handleWrite: ", err)
            writeErrorPacket(conn, prevPacketRec, errNDef, err.Error())
            return 
        }
    }
    
    for prevBytesWritten := blockSize; prevBytesWritten == blockSize ; {
        debugln("TODO remove loopin...")
        (*conn).SetDeadline(timeout())
        n, src ,err := (*conn).ReadFrom(pContent)
        if err != nil {
            e, ok := err.(net.Error)
            if ok && e.Timeout() == true && retryCount < retry {
                retryCount++
                debugln("handleWrite: timed out waiting for client. Retrying send for block", block)
                writeAckPacket(conn, prevPacketRec, block)
                continue
            }
            debugln("handleWrite: ", err)
            writeErrorPacket(conn, p, errNDef, err.Error())
            return 
        }
        packetRec := &sourcedPacket{pContent[:n], src}
        // Check sender TID correctness
        if (packetRec.getTID() != p.getTID()) {
            errMsg := fmt.Sprintf("Unrecognized sender TID %v", filename)
            debugln("handleWrite: ", errMsg)
            writeErrorPacket(conn, packetRec, 5, errMsg)
            continue
        }
        // Check OpCode correctness
        if op, _ := packetRec.getOpcode(); op != opData {
            errMsg := fmt.Sprintf("Opcode %v is invalid in this context.", op)
            debugln("handleWrite:", errMsg)
            writeErrorPacket(conn, packetRec, errIllegalOp, errMsg)
            return
        } 
        // CHeck Block correctness (duplicate or next block)
        pBlock, err := packetRec.getBlock()
        if err != nil {
            debugln("handleWrite:", err)
            writeErrorPacket(conn, packetRec, errNDef, err.Error())
            return
        }
        if pBlock < block {
            // This could be an old duplicate that was lost in the network. Ignoring. 
            continue;
        }
        if pBlock == block {
            // don't reset retryCount before responding to duplicates 
            for err:= writeAckPacket(conn, packetRec, block); err != nil ; {
                if retryCount < retry {
                    retryCount++
                    debugln("handleWrite: Retrying packet send for block", block)
                    err = writeAckPacket(conn, packetRec, block)
                } else {
                    debugln("handleWrite: ", err)
                    writeErrorPacket(conn, packetRec, errNDef, err.Error())
                    return 
                }
            }
            continue;
        }
        block++
        if pBlock != block {
            errMsg := fmt.Sprintf("Unexpected block # %v; was expecting %v", pBlock, block)
            debugln("handleWrite:", errMsg)
            writeErrorPacket(conn, packetRec, errNDef, errMsg)
            return
        }
        pdata, _ := packetRec.getData()
        bytesWritten, _ := newFileDataBuf.Write(pdata)
        prevBytesWritten = bytesWritten
        prevPacketRec = packetRec
        retryCount = 0
        for err:= writeAckPacket(conn, packetRec, block); err != nil ; {
            if retryCount < retry {
                retryCount++
                debugln("handleWrite: Retrying packet send for block", block)
                err = writeAckPacket(conn, packetRec, block)
            } else {
                debugln("handleWrite: ", err)
                writeErrorPacket(conn, packetRec, errNDef, err.Error())
                return 
            }
        }
    }
    debugln("handleWrite: Data transfer complete. Updating file data")
    file.data = newFileDataBuf.Bytes()
    // TODO: listen a little longer to make sure the client doesn't need us to re-ACK the last DATA packet
    return
}

// Packet Sending Functions

/* 
 * Use the given conn to send an Data packet in response to packet sp (based on sender address)
 */ 
func writeDataPacket(conn *net.PacketConn, p *sourcedPacket, block uint16, data []byte) error {
    var b bytes.Buffer
    if err := binary.Write(&b, binary.BigEndian, opData); err != nil {
        debugln("writeDataPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    if err := binary.Write(&b, binary.BigEndian, block); err != nil {
        debugln("writeDataPacket: Error writing block into packet buffer. Error was ", err);
        return err
    }
    b.Write([]byte(data))
    content := (&b).Bytes()
    debugln("writeDataPacket: content is", string(content));
    (*conn).SetDeadline(timeout())
    if _, err := (*conn).WriteTo(content, p.src); err != nil {
        debugln("writeDataPacket: Error sending packet. Error was ", err);
        return err
    }
    return nil
}

/* 
 * Use the given conn to send an Ack packet in response to packet sp (based on sender address)
 */ 
func writeAckPacket(conn *net.PacketConn, p *sourcedPacket,  block uint16) error {
    var b bytes.Buffer
    if err := binary.Write(&b, binary.BigEndian, opAck); err != nil {
        debugln("writeAckPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    if err := binary.Write(&b, binary.BigEndian, block); err != nil {
        debugln("writeAckPacket: Error writing block into packet buffer. Error was ", err);
        return err
    }
    content := (&b).Bytes()
    debugln("writeAckPacket: content is", string(content));
    (*conn).SetDeadline(timeout())
    if _, err := (*conn).WriteTo(content, p.src); err != nil {
        debugln("writeAckPacket: Error sending packet. Error was ", err);
        return err
    }
    return nil
}

/* 
 * Use the given conn to send an Error packet in response to packet sp (based on sender address)
 */ 
func writeErrorPacket(conn *net.PacketConn, p *sourcedPacket, errCode uint16, errMsg string ) error {
    var b bytes.Buffer
    if err := binary.Write(&b, binary.BigEndian, opErr); err != nil {
        debugln("writeErrorPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    if err := binary.Write(&b, binary.BigEndian, errCode); err != nil {
        debugln("writeErrorPacket: Error writing opcode into packet buffer. Error was ", err);
        return err
    }
    b.Write([]byte(errMsg))
    b.WriteByte(byte(0))
    content := (&b).Bytes()
    debugln("writeErrorPacket: content is", string(content));
    (*conn).SetDeadline(timeout())
    if _, err := (*conn).WriteTo(content, p.src); err != nil {
        debugln("writeErrorPacket: Error sending packet. Error was ", err);
        return err
    }
    return nil
}

/*
 * Global vars for runtime configuration
 */
 
// Can we overwrite pre-existing files?
// TODO: add support for setting this via command line
var overwriteEnabled = false;
// Lock on read?
// If true, a write request will block until all read requests on a resource have fully completed, and vice versa, ensuring that all read requests concurrent read requests see the same view of the data.
// If false, reads will be non blocking. Read requests will still return a self-consistent view of the file, but the data returned may be out of date by the time the client receives it.
// TODO: add support for setting this via command line
var lockFileForRead = true;
 
// Lightweight debug logging support
var debug = false;

func debugln(a ...interface{}) (n int, err error) {
    if debug {
        return fmt.Println(a)
    } else {
        return 0, nil
    }
}

/* 
 * main function performs some initialization tasks and then waits on console input
 */
func main() {
    switch opsys:= runtime.GOOS; opsys {
    case "windows":
        // Do nothing for now
    default:
        fmt.Printf("This application has not yet been tested on OS %s. You have been warned.\n", opsys)
    }
    // Only supported arg is "d" for debug/verbose mode
    args := os.Args[1:]
    if len(args) > 0 && args[0] == "d" {
        debug = true
        debugln("Debug output is on.")
    }
    fmt.Printf("Starting TFTP server...\n")
    // Initialize in-memory file system
    fs := make(InMemFS)
    // Since its just an in memory map, no need to defer any cleanup tasks
    
    // Start up request listener routine
    quit := make(chan bool)
    go listen(&fs, quit)
    
    // The main thread will listen for a 'shutdown' command on standard input
    for {
        var input string
        _, inputErr := fmt.Scanln(&input)
        if inputErr == nil {
            // handle input
            switch input {
                case "d":
                    debug = !debug
                    fmt.Println("Set debug mode: ", debug)
                case "ls":
                    fmt.Println(fs)
                case "shutdown":
                    quit <- true
                default:
                    debugln("Unrecognized console input",input)
            }
        } else {
            fmt.Println("Error reading console input: ", inputErr)
        }
        time.Sleep(1 * time.Second)
    }
}