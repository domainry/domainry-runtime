package runtime

func mustCompleteRuntimeStartup(err error) {
	if err != nil {
		panic(err)
	}
}
