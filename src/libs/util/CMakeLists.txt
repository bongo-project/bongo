
add_library(bongoutil SHARED
    pools.c
    memstack.c
	array.c
	fileio.c
	hashtable.c
	bongothreadpool.c
	bongokeyword.c
	lists.c
	stringbuilder.c
	quicksort.c
	utf7mod.c
	logging.c
	bongoagent.c
	bongoutil.c)

target_link_libraries(bongoutil
    ${GLIB2_LIBRARIES}
)

install(TARGETS bongoutil DESTINATION ${LIB_INSTALL_DIR})
