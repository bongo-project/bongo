add_executable(bongoqueue
	conf.c
	domain.c
	queued.c
	mime.c
	queue.c
)

target_link_libraries(bongoqueue
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
)

install(TARGETS bongoqueue DESTINATION ${SBIN_INSTALL_DIR})
