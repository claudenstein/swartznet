# docs/Makefile — build the SwartzNet whitepaper PDF.
#
#   make whitepaper   # compile the committed whitepaper.tex -> whitepaper.pdf
#   make regen        # re-derive whitepaper.tex from whitepaper.md (needs pandoc), then compile
#   make clean        # remove LaTeX aux files (keeps whitepaper.pdf)
#
# whitepaper.tex is self-contained and committed, so `whitepaper` needs only a
# TeX distribution (latexmk). `regen` re-derives the LaTeX from the
# authoritative whitepaper.md and additionally needs pandoc.
LATEXMK ?= latexmk

.PHONY: whitepaper regen clean
whitepaper: whitepaper.pdf
whitepaper.pdf: whitepaper.tex
	$(LATEXMK) -pdf -interaction=nonstopmode -halt-on-error whitepaper.tex
regen:
	./build-whitepaper.sh
clean:
	$(LATEXMK) -c whitepaper.tex
