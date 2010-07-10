StrictCompile()

add_executable(bongo-manager
	manager.c
)

target_link_libraries(bongo-manager
	bongoconnio
	bongomsgapi
	bongojson
	bongoutil
	bongoxpl
	bongocal
)

install(TARGETS bongo-manager DESTINATION ${SBIN_INSTALL_DIR})
