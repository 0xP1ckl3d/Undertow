#ifndef SIFT_VALIDATORS_H
#define SIFT_VALIDATORS_H

/* Returns 0 for rejected, 1 for validated, and 2 for an upstream validator
   that has no native implementation yet. */
static int sift_validate(const char *name, const unsigned char *bytes, size_t length) {
    char value[2049], digits[40];
    size_t i, n = 0;
    int sum;
    if (!name || !*name) return 1;
    if (length > 2048) return 2;
    memcpy(value, bytes, length);
    value[length] = '\0';
    if (strcmp(name, "Luhn") == 0 || strcmp(name, "AustralianTfn") == 0 ||
        strcmp(name, "AustralianMedicare") == 0) {
        for (i = 0; i < length; ++i) {
            if (value[i] >= '0' && value[i] <= '9') {
                if (n >= sizeof(digits)) return 0;
                digits[n++] = value[i];
            }
        }
        if (strcmp(name, "Luhn") == 0) {
            int alternate = 0;
            if (n < 13 || n > 19) return 0;
            sum = 0;
            while (n > 0) {
                int digit = digits[--n] - '0';
                if (alternate) { digit *= 2; if (digit > 9) digit -= 9; }
                sum += digit;
                alternate = !alternate;
            }
            return sum % 10 == 0;
        }
        if (strcmp(name, "AustralianTfn") == 0) {
            static const int weights[] = {1,4,3,7,5,8,6,9,10};
            if (n != 9) return 0;
            sum = 0;
            for (i = 0; i < n; ++i) sum += (digits[i] - '0') * weights[i];
            return sum % 11 == 0;
        }
        {
            static const int weights[] = {1,3,7,9,1,3,7,9};
            if (n < 10 || n > 11 || digits[0] < '2' || digits[0] > '6') return 0;
            sum = 0;
            for (i = 0; i < 8; ++i) sum += (digits[i] - '0') * weights[i];
            return sum % 10 == digits[8] - '0';
        }
    }
    if (strcmp(name, "Iban") == 0) {
        int remainder = 0;
        for (i = 0; i < length; ++i) {
            if (isalnum((unsigned char)value[i])) {
                if (n >= 34) return 0;
                digits[n++] = (char)toupper((unsigned char)value[i]);
            }
        }
        if (n < 15 || n > 34 || !isupper((unsigned char)digits[0]) ||
            !isupper((unsigned char)digits[1]) || !isdigit((unsigned char)digits[2]) ||
            !isdigit((unsigned char)digits[3])) return 0;
        for (i = 0; i < n; ++i) {
            unsigned char c = (unsigned char)digits[(i + 4) % n];
            if (isdigit(c)) remainder = (remainder * 10 + c - '0') % 97;
            else if (isupper(c)) remainder = (remainder * 100 + c - 'A' + 10) % 97;
            else return 0;
        }
        return remainder == 1;
    }
    if (strcmp(name, "GitHubPat") == 0) {
        const char *suffix = NULL;
        static const char *prefixes[] = {"ghp_","gho_","ghu_","ghs_","ghr_",NULL};
        if (strncmp(value, "github_pat_", 11) == 0) {
            suffix = value + 11;
            if (strlen(suffix) < 82) return 0;
            for (i = 0; suffix[i]; ++i)
                if (!isalnum((unsigned char)suffix[i]) && suffix[i] != '_') return 0;
            return 1;
        }
        for (i = 0; prefixes[i]; ++i)
            if (strncmp(value, prefixes[i], 4) == 0) { suffix = value + 4; break; }
        if (!suffix || strlen(suffix) < 36 || strlen(suffix) > 255) return 0;
        for (i = 0; suffix[i]; ++i) if (!isalnum((unsigned char)suffix[i])) return 0;
        return 1;
    }
    if (strcmp(name, "SlackToken") == 0) {
        const char *p = value, *dash;
        if (!(strncmp(p,"xoxb-",5)==0 || strncmp(p,"xoxp-",5)==0 ||
              strncmp(p,"xoxa-",5)==0 || strncmp(p,"xoxr-",5)==0 ||
              strncmp(p,"xoxs-",5)==0 || strncmp(p,"xoxe-",5)==0)) return 0;
        dash = strchr(p + 5, '-');
        if (!dash || strlen(dash + 1) < 10) return 0;
        for (i = 5; value[i]; ++i)
            if (!isalnum((unsigned char)value[i]) && value[i] != '-') return 0;
        return 1;
    }
    return 2;
}

#endif
