install(DIRECTORY ./director
    DESTINATION "${PYTHON_SITEPACKAGES_PATH}/director"
    FILES_MATCHING PATTERN "*.py"
    PATTERN ".svn" EXCLUDE
    )

configure_file(${CMAKE_CURRENT_SOURCE_DIR}/bongodirector.py ${CMAKE_CURRENT_BINARY_DIR}/bongodirector)
install(PROGRAMS ${CMAKE_CURRENT_BINARY_DIR}/bongodirector DESTINATION ${SBIN_INSTALL_DIR})
