SERVICES := svc-1 svc-2 svc-3 svc-4 svc-5 svc-6 svc-7 svc-8
BINS     := $(addprefix out/,$(SERVICES))

.PHONY: all clean $(SERVICES)

all: $(BINS)

$(BINS): out/%: %/*.go | out/
	go build -o $@ ./$*

out/:
	mkdir -p out

clean:
	rm -rf out
