#pragma once
#include <Windows.h>

// XOR encryption/decryption key
constexpr BYTE OBFUSCATION_KEY = 0x7A;

// Compile-time XOR character
constexpr char xor_char(char c, BYTE key) {
    return static_cast<char>(c ^ key);
}

// Compile-time XOR string encryption - use explicit template parameter
template<size_t N>
class ObfuscatedString {
private:
    char encrypted[N];

public:
    constexpr ObfuscatedString(const char(&str)[N]) : encrypted{} {
        for (size_t i = 0; i < N; ++i) {
            encrypted[i] = xor_char(str[i], OBFUSCATION_KEY);
        }
    }

    // Decrypt at runtime into provided buffer
    void decode(WCHAR* buffer, size_t bufferSize) const {
        size_t len = N - 1;
        for (size_t i = 0; i < len && i < bufferSize - 1; ++i) {
            buffer[i] = static_cast<WCHAR>(encrypted[i] ^ OBFUSCATION_KEY);
        }
        buffer[len] = L'\0';
    }

    // For narrow string decoding
    void decodeA(char* buffer, size_t bufferSize) const {
        size_t len = N - 1;
        for (size_t i = 0; i < len && i < bufferSize - 1; ++i) {
            buffer[i] = encrypted[i] ^ OBFUSCATION_KEY;
        }
        buffer[len] = '\0';
    }
};

// Helper macro to create ObfuscatedString with explicit size
#define MAKE_OBFUSCATED(str) ObfuscatedString<sizeof(str)>(str)

