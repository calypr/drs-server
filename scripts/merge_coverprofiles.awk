BEGIN {
	mode = "atomic"
}

FNR == 1 {
	if ($1 != "mode:") {
		print "invalid coverage profile header in " FILENAME > "/dev/stderr"
		exit 1
	}
	if ($2 != mode) {
		print "coverage mode mismatch in " FILENAME ": " $2 > "/dev/stderr"
		exit 1
	}
	next
}

{
	block = $1
	if (!(block in statements)) {
		order[++blocks] = block
		statements[block] = $2
		counts[block] = $3
	}
	if ($3 > counts[block]) {
		counts[block] = $3
	}
}

END {
	if (blocks == 0) {
		exit 1
	}
	print "mode: " mode
	for (i = 1; i <= blocks; i++) {
		block = order[i]
		print block, statements[block], counts[block]
	}
}
