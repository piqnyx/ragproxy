cmake_minimum_required(VERSION 3.10)
project(ragproxy)

# Найти Go
find_program(GO_EXECUTABLE /usr/local/go/bin/go)

if(NOT GO_EXECUTABLE)
    message(FATAL_ERROR "Go executable not found!")
endif()

# Директория исходников
set(GO_SOURCE_DIR ${CMAKE_SOURCE_DIR}/src)

# Директория для бинарников
set(CMAKE_RUNTIME_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/bin)

# Имя исполняемого файла
set(EXE_NAME ragproxy)
if(WIN32)
    set(EXE_NAME ${EXE_NAME}.exe)
endif()

set(OUTPUT_PATH ${CMAKE_RUNTIME_OUTPUT_DIRECTORY}/${EXE_NAME})

# Собираем список исходников Go (чтобы CMake отслеживал изменения)
file(GLOB_RECURSE GO_SOURCES RELATIVE ${CMAKE_SOURCE_DIR} "${GO_SOURCE_DIR}/*.go")

# Команда сборки Go‑проекта
add_custom_command(
    OUTPUT ${OUTPUT_PATH}
    COMMAND ${GO_EXECUTABLE} build
    -gcflags "all=-trimpath=${GO_SOURCE_DIR}"
    -trimpath
    -v
    -o ${OUTPUT_PATH}
    ${GO_SOURCE_DIR}
    COMMENT "Building Go project"
    WORKING_DIRECTORY ${GO_SOURCE_DIR}
    DEPENDS ${GO_SOURCES}
)

# Цель сборки
add_custom_target(go_build ALL
    DEPENDS ${OUTPUT_PATH}
)

# Цель запуска
add_custom_target(go_run
    COMMAND ${OUTPUT_PATH}
    WORKING_DIRECTORY ${GO_SOURCE_DIR}
    COMMENT "Running Go project"
    DEPENDS go_build
)
