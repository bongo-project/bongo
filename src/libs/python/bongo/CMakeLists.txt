# should look at doing a 'real' python install
# http://www.python.org/doc/2.2.3/dist/setup-script.html

install(DIRECTORY ./ 
	DESTINATION "${PYTHON_SITEPACKAGES_PATH}/bongo/" 
	FILES_MATCHING PATTERN "*.py"
	PATTERN ".svn" EXCLUDE
	)
