add_executable(bongorules
	rules.c
	stream.c
)

target_link_libraries(bongorules
	bongoxpl
	bongoconnio
	bongoutil
	bongojson
	bongomsgapi
)

install(TARGETS bongorules DESTINATION ${SBIN_INSTALL_DIR})
