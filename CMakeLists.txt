# Global build file for Bongo
project(bongo C)

# set version of cmake required, and module path
cmake_minimum_required(VERSION 2.6)
cmake_policy(VERSION 2.6)
set(CMAKE_MODULE_PATH ${CMAKE_MODULE_PATH} "${CMAKE_SOURCE_DIR}/cmake/")

set(BONGO_V_MAJOR 0)
set(BONGO_V_MINOR 1)
set(BONGO_V_BUILD 0)
set(BONGO_V_STR "${BONGO_V_MAJOR}.${BONGO_V_MINOR}.${BONGO_V_BUILD}")

# arguments we can supply to the build system - TODO
set(BONGO_USER "root" CACHE STRING "User account Bongo should run under")
option(CONN_TRACE "Enable connection tracing" OFF)
include(cmake/BongoCompiler.cmake)

# find our various build requirements
include(cmake/Requirements.cmake)

# set up defines for build directories
include(cmake/Directories.cmake)
include(cmake/DefineInstallationPaths.cmake)

# define RPATH for debug/etc. builds - allows us to install libraries
# in non-system location and the binaries still link to them
set(CMAKE_INSTALL_RPATH "${XPL_DEFAULT_LIB_DIR}")
set(CMAKE_INSTALL_RPATH_USE_LINK_PATH TRUE)

# any legacy defines which we want to get rid of eventually
include(cmake/Legacy.cmake)

# output header files etc. specifically configured for this build
configure_file(config.h.cmake include/config.h @ONLY)
configure_file(src/agents/store/sql/create-store.s.cmake src/agents/store/sql/createstore.s @ONLY)
configure_file(src/agents/store/sql/create-cookie-1.s.cmake src/agents/store/sql/createcookie-1.s @ONLY)

# tell compiler where to find Bongo's header files
include_directories(BEFORE ${CMAKE_CURRENT_SOURCE_DIR}/include/)
include_directories(BEFORE ${CMAKE_CURRENT_BINARY_DIR}/include/)

# now build our various libraries, agents, binaries.
# DO NOT ALTER (SO)VERSION ON LIBRARIES WITHOUT ASKING!
foreach (LIBRARY xpl streamio msgapi connio util json cal)
	add_subdirectory (src/libs/${LIBRARY})
	set_target_properties(bongo${LIBRARY} PROPERTIES
	        SOVERSION 0 
		VERSION ${BONGO_V_STR}
	)
endforeach (LIBRARY)

add_subdirectory(src/libs/python/libbongo)
add_subdirectory(src/libs/python/bongo)

foreach (AGENT antispam avirus director imap pop queue rules smtp store)
	add_subdirectory (src/agents/${AGENT})
endforeach (AGENT)

foreach (EXECUTABLE config manager testtool queuetool storetool sendmail)
	add_subdirectory (src/apps/${EXECUTABLE})
endforeach (EXECUTABLE)

add_subdirectory (init)

# install various other files
install(DIRECTORY include/
	DESTINATION "${CMAKE_INSTALL_PREFIX}/include/bongo" 
	FILES_MATCHING PATTERN "*.h"
	PATTERN ".svn" EXCLUDE)

install(DIRECTORY zoneinfo 
	DESTINATION ${XPL_DEFAULT_DATA_DIR} 
	FILES_MATCHING PATTERN "*.zone"
	PATTERN ".svn" EXCLUDE)

# include rules for make distribution tarballs
include(cmake/Distribution.cmake)
