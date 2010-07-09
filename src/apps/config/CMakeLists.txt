add_executable(bongo-config
	config.c
	store.c
)

target_link_libraries(bongo-config
	bongoconnio
	bongomsgapi
	bongojson
	bongoutil
	bongoxpl
	bongocal
	${GCRYPT_LIBRARIES}
)

add_custom_target(default.set
	COMMAND tar chof - --exclude .svn config > ${CMAKE_CURRENT_BINARY_DIR}/default.set
	WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}
	VERBATIM
)

add_dependencies(bongo-config default.set)

install(TARGETS bongo-config DESTINATION ${SBIN_INSTALL_DIR})
install(FILES ${CMAKE_CURRENT_BINARY_DIR}/default.set DESTINATION ${DATA_INSTALL_DIR}/conf)
