package logging

import (
	"io"
	"testing"
)

// BenchmarkLogger_FilteredDebug benchmarks debug logging when level is INFO
// This tests the performance improvement of checking level before acquiring lock
func BenchmarkLogger_FilteredDebug(b *testing.B) {
	logger := New(Config{
		Level:     LevelInfo, // Debug messages will be filtered
		Format:    FormatText,
		Output:    io.Discard,
		Component: "bench",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Debug("This message will be filtered out")
	}
}

// BenchmarkLogger_FilteredDebugWithArgs benchmarks debug logging with formatting
func BenchmarkLogger_FilteredDebugWithArgs(b *testing.B) {
	logger := New(Config{
		Level:     LevelInfo,
		Format:    FormatText,
		Output:    io.Discard,
		Component: "bench",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Debug("Message %d with %s formatting", i, "expensive")
	}
}

// BenchmarkLogger_ActiveInfo benchmarks info logging when level is INFO
func BenchmarkLogger_ActiveInfo(b *testing.B) {
	logger := New(Config{
		Level:     LevelInfo,
		Format:    FormatText,
		Output:    io.Discard,
		Component: "bench",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("This message will be logged")
	}
}

// BenchmarkLogger_ActiveInfoWithArgs benchmarks info logging with formatting
func BenchmarkLogger_ActiveInfoWithArgs(b *testing.B) {
	logger := New(Config{
		Level:     LevelInfo,
		Format:    FormatText,
		Output:    io.Discard,
		Component: "bench",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("Message %d with %s formatting", i, "expensive")
	}
}

// BenchmarkLogger_ComponentInheritLevel benchmarks component logger with inherited level
func BenchmarkLogger_ComponentInheritLevel(b *testing.B) {
	SetLevel(LevelInfo)
	logger := NewComponent("bench")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Debug("This message will be filtered out")
	}
}

// BenchmarkLogger_Parallel benchmarks concurrent logging
func BenchmarkLogger_Parallel(b *testing.B) {
	logger := New(Config{
		Level:     LevelInfo,
		Format:    FormatText,
		Output:    io.Discard,
		Component: "bench",
	})

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("Concurrent message")
		}
	})
}

// BenchmarkLogger_ParallelFiltered benchmarks concurrent filtered logging
func BenchmarkLogger_ParallelFiltered(b *testing.B) {
	logger := New(Config{
		Level:     LevelInfo,
		Format:    FormatText,
		Output:    io.Discard,
		Component: "bench",
	})

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Debug("Filtered concurrent message")
		}
	})
}

