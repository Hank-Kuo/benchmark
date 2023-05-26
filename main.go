package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/process"
)

type Metric struct {
	CPUUsage   []float64
	MemUsage   []float32
	MemRSS     []uint64
	WriteBytes []uint64
	ReadBytes  []uint64
	DiskUsage  []float64
	Time       time.Duration
	NumThreads int32
}

type TotalMetric struct {
	MaxCPUUsage float64
	AvgCPUUsage float64
	MaxMemUsage float32
	AvgMemUsage float32
	MaxMemRSS   float64
	AvgMemRSS   float64

	ReadBytes  uint64
	WriteBytes uint64
	DiskUsage  float64
	TotalTime  time.Duration
	NumThreads int32
}

func main() {
	iteration := flag.Int("iteration", 1, "iteration of program")
	flag.Parse()
	args := flag.Args()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	totalMetric := &TotalMetric{}
	done := make(chan struct{})
	handleInterrupt(done)

	for i := 0; i < *iteration; i++ {

		fmt.Printf("\n\033[31m==> Benchmark info (%d) \033[0m\n", i+1)

		cmd := exec.Command(args[0], args[1:]...)

		metric := &Metric{}
		pid := 0
		startTime := time.Now()
		if err := cmd.Start(); err != nil {
			fmt.Printf("Failed to start command: %v\n", err)
			return
		}
		pid = cmd.Process.Pid

		go monitorUsageFromPID(cmd.Process.Pid, ticker.C, done, metric)

		// Wait for the command to finish
		if err := cmd.Wait(); err != nil {
			fmt.Printf("Command failed: %v\n", err)
		}

		endTime := time.Now()
		metric.Time = endTime.Sub(startTime)

		var maxCPU, avgCPU float64
		for _, v := range metric.CPUUsage {
			avgCPU += v
			if maxCPU < v {
				maxCPU = v
			}
		}
		var maxMem, avgMem float32
		for _, v := range metric.MemUsage {
			avgMem += v
			if maxMem < v {
				maxMem = v
			}
		}

		var maxMemRSS, avgMemRSS uint64
		for _, v := range metric.MemRSS {
			avgMemRSS += v
			if maxMemRSS < v {
				maxMemRSS = v
			}
		}

		var maxDiskUsage float64
		for _, v := range metric.DiskUsage {
			if maxDiskUsage < v {
				maxDiskUsage = v
			}
		}

		var maxReadBytes uint64
		for _, v := range metric.ReadBytes {
			if maxReadBytes < v {
				maxReadBytes = v
			}
		}

		var maxWriteBytes uint64
		for _, v := range metric.WriteBytes {
			if maxWriteBytes < v {
				maxWriteBytes = v
			}
		}
		fmt.Println("\n\033[32mProcess detail: \033[0m")
		fmt.Printf("   Command PID: %d\n", pid)
		fmt.Printf("   Execution Time: %v\n", metric.Time)
		fmt.Printf("   Number of thread: %v\n", metric.NumThreads)
		fmt.Println("\n\033[32mCPU detail: \033[0m")
		fmt.Printf("   Max CPU Usage: %.2f %%\n", maxCPU)
		fmt.Printf("   Avg CPU Usage: %.2f %%\n", avgCPU/float64(len(metric.CPUUsage)))
		fmt.Println("\n\033[32mMemory detail: \033[0m")
		fmt.Printf("   Max Mem Usage: %.2f %%\n", maxMem)
		fmt.Printf("   Avg Mem Usage: %.2f %%\n", avgMem/float32(len(metric.MemUsage)))
		fmt.Printf("   Max Mem RSS: %.f \n", float64(maxMemRSS))
		fmt.Printf("   Avg Mem RSS: %.f \n", float64(avgMemRSS)/float64(len(metric.MemRSS)))
		fmt.Println("\n\033[32mI/O detail: \033[0m")
		fmt.Printf("   I/O Read: %.f bytes\n", float32(maxReadBytes))
		fmt.Printf("   I/O Write: %.f bytes\n", float32(maxWriteBytes))
		fmt.Printf("   I/O Usage: %.4f %%\n", maxDiskUsage)

		totalMetric.TotalTime += metric.Time
		totalMetric.NumThreads += metric.NumThreads
		totalMetric.MaxCPUUsage += maxCPU
		totalMetric.AvgCPUUsage += avgCPU / float64(len(metric.CPUUsage))
		totalMetric.MaxMemUsage += maxMem
		totalMetric.AvgMemUsage += avgMem / float32(len(metric.MemUsage))
		totalMetric.MaxMemRSS += float64(maxMemRSS)
		totalMetric.AvgMemRSS += float64(avgMemRSS) / float64(len(metric.MemRSS))
		totalMetric.ReadBytes += maxReadBytes
		totalMetric.WriteBytes += maxWriteBytes
		totalMetric.DiskUsage += maxDiskUsage
		time.Sleep(time.Millisecond * 500)
	}

	renderTable(totalMetric, strings.Join(args[:], " "), *iteration)
}

func monitorUsageFromPID(pid int, ticker <-chan time.Time, done chan struct{}, metric *Metric) {
	proc, err := process.NewProcess(int32(pid))

	if err != nil {
		fmt.Println("Failed to create process:", err)
		return
	}

	th, err := proc.NumThreads()
	if err != nil {
		fmt.Println("Failed to get thread:", err)
		return
	}
	metric.NumThreads = th
	previousIOCounters, err := disk.IOCounters()
	if err != nil {
		fmt.Println("Failed to IO:", err)
		return
	}
	previousDiskUsage, err := disk.Usage("/")
	if err != nil {
		fmt.Println("Failed to get diskUsage:", err)
		return
	}

	for {
		select {
		case <-done:
			fmt.Println("Stop monitor...")
			return
		case <-ticker:
			cpuPercent, err := proc.CPUPercent()
			if err != nil {
				return
			}
			memPercent, err := proc.MemoryPercent()
			if err != nil {
				return
			}
			memoryInfo, err := proc.MemoryInfo()
			if err != nil {
				return
			}
			currentIOCounters, err := disk.IOCounters("disk0")
			if err != nil {
				return
			}
			currentDiskUsage, err := disk.Usage("/")
			if err != nil {
				return
			}

			d := currentDiskUsage.UsedPercent - previousDiskUsage.UsedPercent
			r := currentIOCounters["disk0"].ReadBytes - previousIOCounters["disk0"].ReadBytes
			w := currentIOCounters["disk0"].WriteBytes - previousIOCounters["disk0"].WriteBytes

			metric.DiskUsage = append(metric.DiskUsage, d)
			metric.WriteBytes = append(metric.WriteBytes, w)
			metric.ReadBytes = append(metric.ReadBytes, r)
			metric.CPUUsage = append(metric.CPUUsage, cpuPercent)
			metric.MemUsage = append(metric.MemUsage, memPercent)
			metric.MemRSS = append(metric.MemRSS, memoryInfo.RSS)
		}
	}

}

func handleInterrupt(done chan struct{}) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		close(done)
		os.Exit(0)
	}()
}

func renderTable(m *TotalMetric, command string, iteration int) {
	h, err := host.Info()
	if err != nil {
		fmt.Println("Can't get host info ", err)
	}

	// render
	fmt.Println()
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetTitle("Report Benchmark")

	t.AppendHeader(table.Row{"#", "Name", "Value"})
	t.AppendSeparator()

	t.AppendSeparator()

	t.AppendRows([]table.Row{
		{"Process detail", "OS", h.OS},
		{"", "HostName", h.Hostname},
		{"", "Arch", h.KernelArch},
		{"", "CommadLine", command},
		{"", "Iteration", iteration},
		{"", "Avg Execution Time", m.TotalTime / time.Duration(iteration)},
		{"", "Total Execution Time", m.TotalTime},
		{"", "Avg Number of thread", m.NumThreads / int32(iteration)},
	})
	t.AppendSeparator()

	t.AppendRows([]table.Row{
		{"CPU detail", "Avg Max CPU Usage", fmt.Sprintf("%.2f %%", m.MaxCPUUsage/float64(iteration))},
		{"", "Avg Avg CPU Usage", fmt.Sprintf("%.2f %%", m.AvgCPUUsage/float64(iteration))},
	})
	t.AppendSeparator()

	t.AppendRows([]table.Row{
		{"Memory detail", "Avg Max Mem Usage", fmt.Sprintf("%.2f %%", m.MaxMemUsage/float32(iteration))},
		{"", "Avg Avg Mem Usage", fmt.Sprintf("%.2f %%", m.AvgMemUsage/float32(iteration))},
		{"", "Avg Max Mem RSS", fmt.Sprintf("%.f ", m.MaxMemRSS/float64(iteration))},
		{"", "Avg Avg Mem RSS", fmt.Sprintf("%.f ", m.AvgMemRSS/float64(iteration))},
	})

	t.AppendSeparator()

	t.AppendRows([]table.Row{
		{"I/O detail", "Avg I/O Read", fmt.Sprintf("%.f bytes", float32(m.ReadBytes)/float32(iteration))},
		{"", "Avg I/O Write", fmt.Sprintf("%.f bytes", float32(m.WriteBytes)/float32(iteration))},
		{"", "Avg I/O Usage", fmt.Sprintf("%.4f %%", m.DiskUsage/float64(iteration))},
	})

	t.Render()
}
