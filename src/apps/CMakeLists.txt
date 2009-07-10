add_executable(bongo-
)

target_link_libraries(bongo-
	bongonmap
	bongomsgapi
	bongojson
	bongoutil
	bongoxpl
	bongocal
)

install(TARGETS bongo- DESTINATION ${SBIN_INSTALL_DIR})
