# function to attempt to turn on as many security flags as possible
# by default, we turn on as much as possible

include(CheckCCompilerFlag)

option(BONGO_SECURITY "Enable compiler flags which help protect against attacks" ON)
if (BONGO_SECURITY)
	# check for -lssp ?
	
	# check for -fstack-protector
	check_c_compiler_flag (-fstack-protector-all GCC_STACK_PROTECTOR_ALL)
	if (GCC_STACK_PROTECTOR_ALL)
		set (CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -fstack-protector-all" PARENT_SCOPE)
		set (CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -fstack-protector-all" PARENT_SCOPE)
	endif (GCC_STACK_PROTECTOR_ALL)
endif (BONGO_SECURITY)

option(DEBUG "Enable debugging infrastructure" OFF)
if (DEBUG)
	set (CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -g -g3 -ggdb -O0" PARENT_SCOPE)
	set (CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -g -g3 -ggdb -O0" PARENT_SCOPE)
endif (DEBUG)

function (StrictCompile)
	set (CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -Wall -Wextra -Werror -std=c99 -D_XOPEN_SOURCE=500 -D_POSIX_C_SOURCE=200112 -D_GNU_SOURCE" PARENT_SCOPE)
	set (CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -Wall -Wextra -Werror -std=c++98 -D_XOPEN_SOURCE=500 -D_POSIX_C_SOURCE=200112 -D_GNU_SOURCE" PARENT_SCOPE)
endfunction (StrictCompile)
