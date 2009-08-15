add_executable(bongoavirus
	avirus.c
)

target_link_libraries(bongoavirus
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
	bongologging
)

install(TARGETS bongoavirus DESTINATION ${SBIN_INSTALL_DIR})
