StrictCompile()

add_library(bongomemmgr SHARED
	allocator.c
	pools.c
	memstack.c
)

target_link_libraries(bongomemmgr
	${GLIB2_LIBRARIES}
)

install(TARGETS bongomemmgr DESTINATION ${LIB_INSTALL_DIR})
