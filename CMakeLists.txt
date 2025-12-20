cmake_minimum_required(VERSION 3.10)
project(ragproxy)

find_program(GO_EXECUTABLE /usr/local/go/bin/go)
if(NOT GO_EXECUTABLE)
    message(FATAL_ERROR "Go executable not found!")
endif()

set(GO_SOURCE_DIR ${CMAKE_SOURCE_DIR}/src)
set(CMAKE_CONFIGURATION_TYPES "Debug;Release" CACHE STRING "" FORCE)

set(CMAKE_RUNTIME_OUTPUT_DIRECTORY_DEBUG ${CMAKE_SOURCE_DIR}/build/debug/bin)
set(CMAKE_RUNTIME_OUTPUT_DIRECTORY_RELEASE ${CMAKE_SOURCE_DIR}/build/release/bin)
set(CMAKE_ARCHIVE_OUTPUT_DIRECTORY_DEBUG ${CMAKE_SOURCE_DIR}/build/debug)
set(CMAKE_ARCHIVE_OUTPUT_DIRECTORY_RELEASE ${CMAKE_SOURCE_DIR}/build/release)
set(CMAKE_LIBRARY_OUTPUT_DIRECTORY_DEBUG ${CMAKE_SOURCE_DIR}/build/debug)
set(CMAKE_LIBRARY_OUTPUT_DIRECTORY_RELEASE ${CMAKE_SOURCE_DIR}/build/release)

set(EXE_NAME ragproxy)
if(WIN32)
    set(EXE_NAME ${EXE_NAME}.exe)
endif()

file(GLOB_RECURSE GO_SOURCES RELATIVE ${CMAKE_SOURCE_DIR} "${GO_SOURCE_DIR}/*.go")

add_custom_command(
    OUTPUT ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_DEBUG}/${EXE_NAME}
    COMMAND ${CMAKE_COMMAND} -E env GO111MODULE=on ${GO_EXECUTABLE} build
    -gcflags "all=-N -l"
    -v
    -o ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_DEBUG}/${EXE_NAME}
    .
    COMMENT "Building Go project (Debug)"
    WORKING_DIRECTORY ${GO_SOURCE_DIR}
    DEPENDS ${GO_SOURCES}
)

add_custom_command(
    OUTPUT ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_RELEASE}/${EXE_NAME}
    COMMAND ${CMAKE_COMMAND} -E env GO111MODULE=on ${GO_EXECUTABLE} build
    -ldflags "-s -w"
    -trimpath
    -v
    -o ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_RELEASE}/${EXE_NAME}
    .
    COMMENT "Building Go project (Release)"
    WORKING_DIRECTORY ${GO_SOURCE_DIR}
    DEPENDS ${GO_SOURCES}
)

add_custom_target(Debug ALL
    DEPENDS ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_DEBUG}/${EXE_NAME}
    WORKING_DIRECTORY ${GO_SOURCE_DIR}
)
add_custom_target(Release
    DEPENDS ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_RELEASE}/${EXE_NAME}
    WORKING_DIRECTORY ${GO_SOURCE_DIR}
)


# Импортируемые исполняемые цели для CMake Tools
add_executable(ragproxy-debug IMPORTED GLOBAL)
set_target_properties(Debug PROPERTIES
    IMPORTED_LOCATION ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_DEBUG}/${EXE_NAME}
)
add_dependencies(ragproxy-debug Debug)

add_executable(ragproxy-release IMPORTED GLOBAL)
set_target_properties(ragproxy-release PROPERTIES
    IMPORTED_LOCATION ${CMAKE_RUNTIME_OUTPUT_DIRECTORY_RELEASE}/${EXE_NAME}
)
add_dependencies(ragproxy-release Release)
