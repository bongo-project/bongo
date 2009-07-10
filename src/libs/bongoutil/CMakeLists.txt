
add_library(bongoutil SHARED
	array.c
	fileio.c
	hashtable.c
	bongothreadpool.c
	bongokeyword.c
	lists.c
	stringbuilder.c
	quicksort.c
	utf7mod.c
	bongoutil.c)

install(TARGETS bongoutil DESTINATION ${LIB_INSTALL_DIR})
