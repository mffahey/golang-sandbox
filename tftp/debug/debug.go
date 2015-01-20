// Lightweight debug logging support
package debug

import "fmt"

// Global setting to turn debug on/off
var Debug = false;

func Debugln(a ...interface{}) (n int, err error) {
    if Debug {
        return fmt.Println(a)
    } else {
        return 0, nil
    }
}