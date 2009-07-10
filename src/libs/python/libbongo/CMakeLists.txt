include_directories(AFTER ${PYTHON_INCLUDE_PATH})

python_add_module(libs
	cal.c
	calcmd.c
	libs.c
	msgapi.c
	msgapi-defs.c
	pybongo.c
	streamio.c
	json.c
	tests.c
	bongoutil.c
)

python_add_module(bootstrap 
	bootstrap.c
	msgapi-defs.c
	pybongo.c
)

# remove 'lib' prefix from these libraries.
set_target_properties(libs bootstrap
	PROPERTIES
	PREFIX ""
)

target_link_libraries(libs
	bongonmap
	bongomsgapi
	bongojson
	bongoutil
	bongoxpl
	bongocal
	${PYTHON_LIBRARIES}
)

target_link_libraries(bootstrap
	${PYTHON_LIBRARIES}
)

install(TARGETS libs bootstrap DESTINATION ${PYTHON_SITEPACKAGES_PATH}/libbongo/)
