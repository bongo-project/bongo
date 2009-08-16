# Global build file for Bongo
project(bongo C)

# set version of cmake required, and module path
cmake_minimum_required(VERSION 2.6)
cmake_policy(VERSION 2.6)
set(CMAKE_MODULE_PATH ${CMAKE_MODULE_PATH} "${CMAKE_SOURCE_DIR}/cmake/")

# arguments we can supply to the build system - TODO
set(BONGO_USER "root" CACHE STRING "User account Bongo should run under")
option(CONN_TRACE "Enable connection tracing" OFF)
include(cmake/BongoCompiler.cmake)

# find our various build requirements
include(cmake/Requirements.cmake)

# define RPATH for debug/etc. builds - allows us to install libraries
# in non-system location and the binaries still link to them
set(CMAKE_INSTALL_RPATH "${CMAKE_INSTALL_PREFIX}/lib")
set(CMAKE_INSTALL_RPATH_USE_LINK_PATH TRUE)

# set up defines for build directories
include(cmake/DefineInstallationPaths.cmake)
include(cmake/Directories.cmake)

# any legacy defines which we want to get rid of eventually
include(cmake/Legacy.cmake)

# output header files etc. specifically configured for this build
configure_file(config.h.cmake include/config.h @ONLY)
configure_file(src/agents/store/create-store.s.cmake src/agents/store/createstore.s @ONLY)

# tell compiler where to find Bongo's header files
include_directories(BEFORE ${CMAKE_CURRENT_SOURCE_DIR}/include/)
include_directories(BEFORE ${CMAKE_CURRENT_BINARY_DIR}/include/)

# now build our various libraries, agents, binaries.
foreach (LIBRARY xpl streamio logging msgapi connio bongoutil 
		json cal nmap python/libbongo python/bongo)
	add_subdirectory (src/libs/${LIBRARY})
endforeach (LIBRARY)

foreach (AGENT antispam avirus imap pop queue rules smtp store)
	add_subdirectory (src/agents/${AGENT})
endforeach (AGENT)

foreach (EXECUTABLE config manager testtool queuetool)
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
