
add_library(bongonmap SHARED
	bongoagent.c
	nmap.c
	hashcred.c
	mime.c)

target_link_libraries(bongonmap
	bongoconnio
	bongomemmgr
	bongoxpl
	bongologging
	bongostreamio
	${GNUTLS_LIBRARIES}
	${GCRYPT_LIBRARIES}
	${PTHREAD_LIBRARIES}
	)

install(TARGETS bongonmap DESTINATION ${LIB_INSTALL_DIR})
