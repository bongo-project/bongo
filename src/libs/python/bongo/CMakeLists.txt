# should look at doing a 'real' python install
# http://www.python.org/doc/2.2.3/dist/setup-script.html

configure_file(${CMAKE_CURRENT_SOURCE_DIR}/Xpl.py.in ${CMAKE_CURRENT_BINARY_DIR}/Xpl.py)

install(DIRECTORY ./ 
	DESTINATION "${PYTHON_SITEPACKAGES_PATH}/bongo/" 
	FILES_MATCHING PATTERN "*.py"
	PATTERN ".svn" EXCLUDE
	)

install(FILES ${CMAKE_CURRENT_BINARY_DIR}/Xpl.py DESTINATION ${PYTHON_SITEPACKAGES_PATH}/bongo/)
