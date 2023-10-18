package examples

// DocRlimit {
import "github.com/xiaofsec/ebpf/rlimit"

func main() {
	if err := rlimit.RemoveMemlock(); err != nil {
		panic(err)
	}
}

// }
